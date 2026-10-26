package searchroutes

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/searchdet"
)

// Flow is one route's last real request, as it happened: every step is a
// measured step of that request, never an invented hop. A route that has
// not run has no flow, and the page says so.
type Flow struct {
	RouteID   string `json:"routeId"`
	RouteName string `json:"routeName"`
	// State is "ok" or "failed": what the request came to.
	State string `json:"state"`
	// Source is what produced it: "investigation" (an engine run), "probe"
	// (the route's own real call) or "discovery" (a scan of this computer).
	Source     string `json:"source"`
	At         string `json:"at"`
	DurationMs int    `json:"durationMs"`
	Hits       int    `json:"hits"`
	Steps      []Step `json:"steps"`
	Error      string `json:"error,omitempty"`
}

// Step is one measured step of a flow, at its real offset from the start.
type Step struct {
	Label      string `json:"label"`
	State      string `json:"state"`
	AtMs       int    `json:"atMs"`
	DurationMs int    `json:"durationMs"`
	Hits       int    `json:"hits"`
	Detail     string `json:"detail,omitempty"`
}

// FlowStore keeps the last flow of each route in this process. It is
// memory, not a report file: after a restart the page shows "not run yet",
// which is the truth.
type FlowStore struct {
	mu    sync.Mutex
	flows map[string]Flow
}

// NewFlowStore is an empty store.
func NewFlowStore() *FlowStore { return &FlowStore{flows: map[string]Flow{}} }

// Get is one route's last flow.
func (s *FlowStore) Get(routeID string) (Flow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flows[routeID]
	return f, ok
}

// All is every route's last flow, by route id.
func (s *FlowStore) All() []Flow {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Flow, 0, len(s.flows))
	for _, f := range s.flows {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RouteID < out[j].RouteID })
	return out
}

func (s *FlowStore) put(f Flow) {
	s.mu.Lock()
	s.flows[f.RouteID] = f
	s.mu.Unlock()
}

// RecordInvestigation folds a finished engine run's own trajectory into the
// flow of each provider it asked. A provider that did not run is left as it
// was; a step's state and milliseconds are the ones the engine measured.
func (s *FlowStore) RecordInvestigation(inv searchdet.Investigation) {
	by := map[string][]searchdet.TrajectoryStep{}
	for _, st := range inv.Trajectory {
		if st.ChannelID == "" {
			continue // a step from before the channel id was carried
		}
		by[st.ChannelID] = append(by[st.ChannelID], st)
	}
	for channel, steps := range by {
		f := Flow{RouteID: channel, RouteName: steps[0].Gateway, Source: "investigation", At: time.Now().Format(time.RFC3339)}
		for _, st := range steps {
			state := "ok"
			if st.ReturnSummary != "" && st.ReturnSummary[0] == 0xE2 { // the engine's ❌ prefix
				state = "failed"
			}
			if st.RawHits == 0 && state == "ok" {
				state = "failed"
			}
			f.Steps = append(f.Steps, Step{
				Label: st.Params, State: state, AtMs: st.StartOffsetMs,
				DurationMs: st.DurationMs, Hits: st.RawHits, Detail: st.ReturnSummary,
			})
			if end := st.StartOffsetMs + st.DurationMs; end > f.DurationMs {
				f.DurationMs = end
			}
			f.Hits += st.RawHits
			if state == "failed" && f.Error == "" {
				f.Error = st.ReturnSummary
			}
		}
		f.State = "ok"
		if f.Hits == 0 {
			f.State = "failed"
		}
		s.put(f)
	}
}

// ProbeRoute asks one web route for real, timed here, and records its flow.
// A discovery route has nothing to probe over the network: its scan is
// RecordDiscovery's.
func (s *FlowStore) ProbeRoute(ctx context.Context, route Route, query string) (Flow, error) {
	if route.Channel == "" {
		return Flow{}, fmt.Errorf("%s is a discovery route: scan it instead of probing it", route.ID)
	}
	if query == "" {
		query = "MolaSearch route check"
	}
	start := time.Now()
	n, err := searchdet.ProbeChannel(ctx, route.Channel, query)
	took := time.Since(start)
	f := Flow{
		RouteID: route.Channel, RouteName: route.Name, Source: "probe",
		At: start.Format(time.RFC3339), DurationMs: int(took.Milliseconds()), Hits: n,
		State: "ok",
	}
	if err != nil {
		f.State, f.Error = "failed", err.Error()
	}
	f.Steps = []Step{{Label: query, State: f.State, DurationMs: f.DurationMs, Hits: n, Detail: f.Error}}
	s.put(f)
	return f, err
}

// RecordDiscovery records a real scan: what it found and how long it took.
func (s *FlowStore) RecordDiscovery(kind MarketplaceKind, items []string, took time.Duration, at time.Time) Flow {
	id := "agent-" + string(kind)
	f := Flow{
		RouteID: id, RouteName: MarketplaceRoute(kind, len(items)).Name, Source: "discovery",
		At: at.Format(time.RFC3339), DurationMs: int(took.Milliseconds()), Hits: len(items), State: "ok",
	}
	if len(items) == 0 {
		f.State = "failed"
		f.Error = "nothing found on this computer"
	}
	f.Steps = []Step{{Label: "scan", State: f.State, DurationMs: f.DurationMs, Hits: len(items), Detail: f.Error}}
	s.put(f)
	return f
}
