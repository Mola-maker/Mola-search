package searchroutes

import (
	"testing"

	"github.com/yetone/magpie/internal/searchdet"
)

// TestRegistryNamesTheRoutesAndTheirKeys: the registry is built from the
// engine's channels and the provider platform, so a route without keys is
// not configured and its gate is the user's own switch.
func TestRegistryNamesTheRoutesAndTheirKeys(t *testing.T) {
	routes := Registry(searchdet.Keys{}, []string{"github"}, map[MarketplaceKind]int{MarketSkills: 3})
	byID := map[string]Route{}
	for _, r := range routes {
		byID[r.ID] = r
	}
	if r, ok := byID["github"]; !ok || !r.Configured || r.Keyed || r.Channel != "github" {
		t.Errorf("github is keyless and ready: %+v", r)
	}
	if r := byID["github"]; r.Enabled || r.Channel != "github" {
		t.Errorf("github's gate: %+v", r)
	}
	for _, id := range []string{"exa", "tavily", "tencent"} {
		r := byID[id]
		if r.ID == "" || r.Configured || !r.Keyed || r.Channel != id {
			t.Errorf("%s with no keys: %+v", id, r)
		}
	}
	if r := byID["agent-skills"]; r.Kind != KindMarketplace || r.Detail != "3 found" || !r.Configured {
		t.Errorf("agent-skills: %+v", r)
	}
	for _, k := range MarketplaceKinds {
		if r := byID["agent-"+string(k)]; r.ID == "" {
			t.Errorf("no marketplace route for %s", k)
		}
	}
	// the channels a stack picks are the engine's own ids
	if got := ChannelIDs(routes, []string{"github", "agent-skills", "nope"}); len(got) != 1 || got[0] != "github" {
		t.Errorf("ChannelIDs: %v", got)
	}
}

func cite(title, url, channel string, authority int) searchdet.Citation {
	return searchdet.Citation{Title: title, URL: url, Channel: channel, AuthorityLevel: authority}
}

// TestResolveMergesPagesAndKeepsTheAgentsPrecedence: one page two agents
// found is one citation, and a topic two agents found at different pages
// keeps the page the higher-precedence agent returned.
func TestResolveMergesPagesAndKeepsTheAgentsPrecedence(t *testing.T) {
	results := []AgentResult{
		{Agent: "b", Citations: []searchdet.Citation{
			cite("Same Page", "https://example.com/a?utm_source=x", "exa", 3),
			cite("Wiz buys Google", "https://b.example/wiz", "tavily", 4),
		}},
		{Agent: "a", Citations: []searchdet.Citation{
			cite("Same Page", "https://example.com/a", "github", 2),
			cite("Google buys Wiz", "https://a.example/wiz", "github", 3),
		}},
	}
	res := Resolve("wiz", results, []string{"a", "b"}, map[string]int{"github": 1, "exa": 2, "tavily": 3})
	if len(res.Citations) != 2 {
		t.Fatalf("citations: %+v", res.Citations)
	}
	if res.Duplicates != 1 {
		t.Errorf("the same page from two agents: %d duplicates", res.Duplicates)
	}
	var page MergedCitation
	for _, c := range res.Citations {
		if c.URL == "https://example.com/a" {
			page = c
		}
	}
	if page.URL == "" || len(page.Agents) != 2 || page.Agents[0] != "a" {
		t.Errorf("the same page from two agents is one citation: %+v", res.Citations)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Winner != "https://a.example/wiz" {
		t.Errorf("the claim conflict: %+v", res.Conflicts)
	}
	if res.Conflicts[0].Losers[0] != "https://b.example/wiz" {
		t.Errorf("the loser: %+v", res.Conflicts[0])
	}
	if len(res.Agents) != 2 || res.Agents[0] != "a" {
		t.Errorf("agents in the user's order: %v", res.Agents)
	}
}

// TestResolveRoutePrecedenceOrdersOneAgent: within one agent, the route
// precedence orders the citations, then authority.
func TestResolveRoutePrecedenceOrdersOneAgent(t *testing.T) {
	results := []AgentResult{{Agent: "a", Citations: []searchdet.Citation{
		cite("One", "https://one.example/x", "tavily", 5),
		cite("Two", "https://two.example/x", "github", 1),
		cite("Three", "https://three.example/x", "github", 4),
	}}}
	res := Resolve("q", results, nil, map[string]int{"github": 1, "tavily": 2})
	got := []string{res.Citations[0].URL, res.Citations[1].URL, res.Citations[2].URL}
	want := []string{"https://three.example/x", "https://two.example/x", "https://one.example/x"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// TestResolveIsDeterministic: the same input twice reads the same, however
// the results were given.
func TestResolveIsDeterministic(t *testing.T) {
	a := AgentResult{Agent: "a", Citations: []searchdet.Citation{cite("A thing", "https://a.example/x", "exa", 2)}}
	b := AgentResult{Agent: "b", Citations: []searchdet.Citation{cite("A thing", "https://b.example/x", "tavily", 2)}}
	one := Resolve("q", []AgentResult{a, b}, []string{"a", "b"}, nil)
	two := Resolve("q", []AgentResult{b, a}, []string{"a", "b"}, nil)
	if one.Conflicts[0].Winner != two.Conflicts[0].Winner {
		t.Fatalf("winner %q then %q", one.Conflicts[0].Winner, two.Conflicts[0].Winner)
	}
}
