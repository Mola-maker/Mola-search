package ragstore

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Operation is one real call this process made against a backend. The page's
// live feed draws exactly these: nothing is synthesized, and an event exists
// only because Manager.Search, Manager.Insert, Manager.HotSwap, Manager.Status
// or Manager.Habits actually ran.
type Operation struct {
	ID         int64  `json:"id"`
	At         string `json:"at"`
	Actor      string `json:"actor"`
	Backend    string `json:"backend"`
	Op         string `json:"op"`
	Detail     string `json:"detail"`
	Count      int    `json:"count"`
	OK         bool   `json:"ok"`
	DurationMs int64  `json:"durationMs"`
}

// eventRing is a bounded in-process log: recent operations stay readable for
// a page that connects late, and live ones fan out to every subscriber. It is
// deliberately small — this is a status page's feed, not an audit trail.
type eventRing struct {
	mu     sync.Mutex
	next   int64
	ring   []Operation
	subs   map[chan Operation]struct{}
	closed bool
}

const (
	eventRingSize = 200
	eventBufSize  = 64
)

func newEventRing() *eventRing {
	return &eventRing{subs: map[chan Operation]struct{}{}}
}

func (e *eventRing) add(op Operation) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.next++
	op.ID = e.next
	if op.At == "" {
		op.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	e.ring = append(e.ring, op)
	if len(e.ring) > eventRingSize {
		e.ring = append([]Operation(nil), e.ring[len(e.ring)-eventRingSize:]...)
	}
	for ch := range e.subs {
		select {
		case ch <- op:
		default:
			// A subscriber that does not drain cannot stall a backend call.
			// The ring still holds the event for the next reader.
		}
	}
}

// subscribe returns a buffered channel of live operations and the function
// that removes it.
func (e *eventRing) subscribe() (<-chan Operation, func()) {
	ch := make(chan Operation, eventBufSize)
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	e.subs[ch] = struct{}{}
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		if _, ok := e.subs[ch]; ok {
			delete(e.subs, ch)
			close(ch)
		}
		e.mu.Unlock()
	}
}

// recent returns the newest n operations, oldest first.
func (e *eventRing) recent(n int) []Operation {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n <= 0 || n > len(e.ring) {
		n = len(e.ring)
	}
	out := make([]Operation, n)
	copy(out, e.ring[len(e.ring)-n:])
	return out
}

// since returns every operation with an id greater than id, oldest first.
func (e *eventRing) since(id int64) []Operation {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Operation, 0)
	for _, op := range e.ring {
		if op.ID > id {
			out = append(out, op)
		}
	}
	return out
}

// actorKey carries who asked for an operation through the call chain, so a
// write from an MCP-originated investigation is attributed to that client
// and a page's own call is attributed to magpie. It never invents a name.
type actorKey struct{}

// WithActor names the caller of the operations this context reaches.
func WithActor(ctx context.Context, actor string) context.Context {
	actor = strings.TrimSpace(actor)
	if actor == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorOf is the caller's own name, or "magpie" when nothing named it.
func ActorOf(ctx context.Context) string {
	if ctx != nil {
		if a, ok := ctx.Value(actorKey{}).(string); ok && strings.TrimSpace(a) != "" {
			return strings.TrimSpace(a)
		}
	}
	return "magpie"
}

// note records one operation if this manager has a feed. Duration is the
// wall time the call really took.
func (m *Manager) note(ctx context.Context, backend, op, detail string, count int, ok bool, took time.Duration) {
	if m == nil || m.events == nil {
		return
	}
	m.events.add(Operation{
		Actor:      ActorOf(ctx),
		Backend:    backend,
		Op:         op,
		Detail:     detail,
		Count:      count,
		OK:         ok,
		DurationMs: took.Milliseconds(),
	})
}

// SubscribeOperations streams live operations to one reader.
func (m *Manager) SubscribeOperations() (<-chan Operation, func()) {
	if m == nil || m.events == nil {
		ch := make(chan Operation)
		close(ch)
		return ch, func() {}
	}
	return m.events.subscribe()
}

// RecentOperations returns the newest n operations, oldest first.
func (m *Manager) RecentOperations(n int) []Operation {
	if m == nil || m.events == nil {
		return []Operation{}
	}
	return m.events.recent(n)
}

// OperationsSince returns every operation newer than id, oldest first.
func (m *Manager) OperationsSince(id int64) []Operation {
	if m == nil || m.events == nil {
		return []Operation{}
	}
	return m.events.since(id)
}
