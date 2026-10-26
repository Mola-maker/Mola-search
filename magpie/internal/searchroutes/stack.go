package searchroutes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// AgentStack is one agent's search tech stack: the routes it uses. Source
// is "derived" when it comes from what is really wired into the agent on
// this computer, "user" when the user picked it.
type AgentStack struct {
	Agent  string   `json:"agent"`
	Name   string   `json:"name,omitempty"`
	Routes []string `json:"routes"`
	Source string   `json:"source"`
	// Evidence says where the derived stack was seen (an MCP config file, a
	// skill folder), so a derivation is checkable.
	Evidence  []string `json:"evidence,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
}

// stackFile is search_routes.json as written.
type stackFile struct {
	// Stacks is agent id → route ids, only what the user set.
	Stacks map[string][]string `json:"stacks"`
}

// StackStore keeps the user's per-agent route choices in one file. A file
// that cannot be read is an error, never an empty stack set: unknown is not
// "no routes".
type StackStore struct {
	path string

	mu     sync.Mutex
	stacks map[string][]string
}

// NewStackStore is a store at path, nothing loaded yet.
func NewStackStore(path string) *StackStore {
	return &StackStore{path: path, stacks: map[string][]string{}}
}

// Load reads the file. A file that is not there is empty; one that cannot
// be read or parsed returns the error and keeps what was loaded before.
func (s *StackStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f stackFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("%s doesn't parse: %w", s.path, err)
	}
	if f.Stacks != nil {
		s.stacks = f.Stacks
	}
	return nil
}

// Stacks is a copy of the user's choices, by agent.
func (s *StackStore) Stacks() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]string, len(s.stacks))
	for agent, routes := range s.stacks {
		out[agent] = append([]string(nil), routes...)
	}
	return out
}

// Set records the user's stack for one agent. Every route id must be in
// known, or nothing is written: a typo is refused, not saved as a route
// that never runs.
func (s *StackStore) Set(agent string, routes []string, known map[string]bool) (AgentStack, error) {
	agent = trimSpace(agent)
	if agent == "" {
		return AgentStack{}, fmt.Errorf("no agent given")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(routes))
	for _, id := range routes {
		if !known[id] {
			return AgentStack{}, fmt.Errorf("no search route %q", id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string][]string, len(s.stacks)+1)
	for a, r := range s.stacks {
		next[a] = r
	}
	next[agent] = out
	if err := s.write(next); err != nil {
		return AgentStack{}, err
	}
	s.stacks = next
	return AgentStack{Agent: agent, Routes: out, Source: "user", UpdatedAt: time.Now().Format(time.RFC3339)}, nil
}

// write puts the file in place whole or not at all.
func (s *StackStore) write(stacks map[string][]string) error {
	b, err := json.MarshalIndent(stackFile{Stacks: stacks}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".search-routes-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

// Derive is each installed agent's stack as its own folders show it: an
// agent with MolaSearch's MCP server or Detective skill uses the engine's
// web routes, and every agent that has marketplace items has those routes.
// It reads no file itself; targets and markets are a scan's answer.
func Derive(targets []AgentTarget, markets []Market, routes []Route) []AgentStack {
	byAgent := map[string]Market{}
	for _, m := range markets {
		byAgent[m.Agent] = m
	}
	var web []string
	for _, r := range routes {
		if r.Kind == KindWeb {
			web = append(web, r.ID)
		}
	}
	sort.Strings(web)
	var out []AgentStack
	for _, t := range targets {
		if t.Agent == "" {
			continue
		}
		m := byAgent[t.Agent]
		var evidence []string
		wired := false
		if contains(m.Skills, "molasearch-detective") {
			wired = true
			evidence = append(evidence, filepath.Join(t.Skills, "molasearch-detective"))
		}
		if contains(m.MCP, "molasearch") {
			wired = true
			evidence = append(evidence, t.MCPFiles...)
		}
		var stack []string
		if wired {
			stack = append(stack, web...)
		}
		for _, k := range MarketplaceKinds {
			if m.Count(k) > 0 {
				stack = append(stack, "agent-"+string(k))
			}
		}
		if len(stack) == 0 {
			continue
		}
		sort.Strings(stack)
		out = append(out, AgentStack{Agent: t.Agent, Name: t.Name, Routes: stack, Source: "derived", Evidence: evidence})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent < out[j].Agent })
	return out
}

// Merge lays the user's own stacks over the derived ones. An agent the user
// set is "user"; a derived one keeps its evidence. An agent only the user
// names (not installed now) is still listed, since their choice is real.
func Merge(stored map[string][]string, derived []AgentStack) []AgentStack {
	out := make([]AgentStack, 0, len(derived)+len(stored))
	seen := map[string]bool{}
	for _, d := range derived {
		if routes, ok := stored[d.Agent]; ok {
			d.Routes, d.Source = append([]string(nil), routes...), "user"
			delete(stored, d.Agent)
		}
		seen[d.Agent] = true
		out = append(out, d)
	}
	for agent, routes := range stored {
		if seen[agent] {
			continue
		}
		out = append(out, AgentStack{Agent: agent, Routes: append([]string(nil), routes...), Source: "user"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent < out[j].Agent })
	return out
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
