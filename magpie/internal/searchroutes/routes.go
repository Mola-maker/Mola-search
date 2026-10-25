// Package searchroutes is MolaSearch's search-route registry: every
// reliable way a search can reach the web or this computer, with the truth
// about its keys and its last real request.
//
// Two kinds of route: an engine channel (local GitHub repository search,
// Exa, Tavily, Tencent/Yuanbao SearchPro, Brave, SearXNG, DuckDuckGo,
// Wikipedia and the rest), whose readiness is the presence of its own keys;
// and the agent marketplace on this computer (the skills, MCP servers,
// plugins and scripts the installed agents can actually read). Nothing here
// invents a state: a route with no keys is "not configured", a flow is
// built from a real run's own timings, and discovery lists what is on disk.
package searchroutes

import (
	"sort"

	"github.com/yetone/magpie/internal/providerhub"
	"github.com/yetone/magpie/internal/searchdet"
)

// Kind is what a route reaches.
type Kind string

const (
	KindWeb         Kind = "web"         // an engine search channel
	KindMarketplace Kind = "marketplace" // discovery on this computer
)

// MarketplaceKind is the part of the agent marketplace a discovery route
// lists.
type MarketplaceKind string

const (
	MarketSkills  MarketplaceKind = "skills"
	MarketMCP     MarketplaceKind = "mcp"
	MarketPlugins MarketplaceKind = "plugins"
	MarketScripts MarketplaceKind = "scripts"
)

// MarketplaceKinds is every discovery route, in the order the page shows
// them.
var MarketplaceKinds = []MarketplaceKind{MarketSkills, MarketMCP, MarketPlugins, MarketScripts}

// Route is one reliable path. Configured is the route's own truth: its keys
// are present (or it needs none), or the last scan found marketplace items.
// Enabled is the user's gate, which is a different thing.
type Route struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       Kind     `json:"kind"`
	Channel    string   `json:"channel,omitempty"`
	Keyed      bool     `json:"keyed"`
	Configured bool     `json:"configured"`
	Enabled    bool     `json:"enabled"`
	KeyEnvs    []string `json:"keyEnvs,omitempty"`
	Docs       string   `json:"docs,omitempty"`
	// Precedence orders routes for conflict resolution: lower first. It is
	// fixed by the engine's own channel order, so two runs resolve the same
	// way.
	Precedence int `json:"precedence"`
	// Detail is a measured count for a discovery route (items found), ""
	// for a channel.
	Detail string `json:"detail,omitempty"`
}

// MarketplaceRoute is the descriptor of one discovery route: a search of
// what the installed agents can read.
func MarketplaceRoute(k MarketplaceKind, found int) Route {
	r := Route{ID: "agent-" + string(k), Kind: KindMarketplace, Configured: true, Enabled: true, Precedence: 100 + marketplaceIndex(k)}
	switch k {
	case MarketSkills:
		r.Name = "Agent skills"
	case MarketMCP:
		r.Name = "Agent MCP servers"
	case MarketPlugins:
		r.Name = "Agent plugins"
	case MarketScripts:
		r.Name = "Agent scripts"
	}
	if found >= 0 {
		r.Detail = itoa(found) + " found"
	}
	return r
}

func marketplaceIndex(k MarketplaceKind) int {
	for i, x := range MarketplaceKinds {
		if x == k {
			return i
		}
	}
	return len(MarketplaceKinds)
}

// Registry is every reliable route: the engine's search channels the
// provider platform names (so a route and its keys are one record), then
// the marketplace routes. disabled is the user's per-channel gate
// (searchdet Settings.DisabledChannels). found says how many marketplace
// items the last scan found, -1 when it has not run.
func Registry(k searchdet.Keys, disabled []string, found map[MarketplaceKind]int) []Route {
	off := make(map[string]bool, len(disabled))
	for _, id := range disabled {
		off[id] = true
	}
	meta := map[string]providerhub.Provider{}
	for _, p := range providerhub.All() {
		if p.Category == providerhub.CatSearch && p.Channel != "" {
			meta[p.Channel] = p
		}
	}
	var out []Route
	for i, ci := range searchdet.ChannelInfos(k) {
		r := Route{
			ID: ci.ID, Name: ci.Name, Kind: KindWeb, Channel: ci.ID,
			Keyed: ci.Keyed, Configured: ci.Configured, Enabled: !off[ci.ID], Precedence: i,
		}
		// the provider platform names the ones it wires and knows their key
		// names; a channel it doesn't name is still a real path, with the
		// engine's own name and readiness
		if p, ok := meta[ci.ID]; ok {
			r.ID, r.Name, r.Docs = p.ID, p.Name, p.Docs
			for _, f := range p.Fields {
				r.KeyEnvs = append(r.KeyEnvs, f.Env)
			}
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Precedence < out[j].Precedence })
	for _, kind := range MarketplaceKinds {
		n := -1
		if found != nil {
			if v, ok := found[kind]; ok {
				n = v
			}
		}
		out = append(out, MarketplaceRoute(kind, n))
	}
	return out
}

// RouteOf finds a route by id in a registry.
func RouteOf(routes []Route, id string) (Route, bool) {
	for _, r := range routes {
		if r.ID == id {
			return r, true
		}
	}
	return Route{}, false
}

// ChannelIDs are the engine channels of the given route ids, for a run's
// allow-list. An unknown id or a discovery route is left out.
func ChannelIDs(routes []Route, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	by := map[string]Route{}
	for _, r := range routes {
		by[r.ID] = r
	}
	var out []string
	for _, id := range ids {
		if r, ok := by[id]; ok && r.Channel != "" {
			out = append(out, r.Channel)
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
