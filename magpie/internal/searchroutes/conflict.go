package searchroutes

import (
	"sort"
	"strings"
	"unicode"

	"github.com/yetone/magpie/internal/searchdet"
)

// AgentResult is one agent's search on a topic: the citations its run
// returned, in the order its own engine ranked them.
type AgentResult struct {
	Agent     string               `json:"agent"`
	Query     string               `json:"query,omitempty"`
	At        string               `json:"at,omitempty"`
	Citations []searchdet.Citation `json:"citations"`
}

// MergedCitation is one page several agents found, or one they did not
// share. Agents are the agents that returned it, Routes the providers that
// did; both are sorted so two resolutions of the same input read the same.
type MergedCitation struct {
	searchdet.Citation
	Agents []string `json:"agents"`
	Routes []string `json:"routes,omitempty"`
}

// ClaimConflict is one claim two agents placed at different pages. The one
// kept is the winner: deterministic precedence, not arrival order.
type ClaimConflict struct {
	Claim  string   `json:"claim"`
	Winner string   `json:"winner"`
	Losers []string `json:"losers"`
	Agents []string `json:"agents"`
}

// Resolution is what several agents' searches on one topic come to: each
// page once, and each claim at one page, with the agent and route that won
// it.
type Resolution struct {
	Query     string           `json:"query"`
	Agents    []string         `json:"agents"`
	Citations []MergedCitation `json:"citations"`
	// Duplicates counts citations merged into a page another agent already
	// returned.
	Duplicates int             `json:"duplicates"`
	Conflicts  []ClaimConflict `json:"conflicts"`
}

// Resolve merges several agents' results for one topic. The precedence is
// deterministic and does not depend on map order: the agent order given
// (the user's stack order, then an agent id not in it), then a route's own
// precedence in the registry, then the citation's authority, then its URL.
// A page two agents found is one citation carrying both; a claim two agents
// placed at different pages keeps the higher-precedence page and records
// the conflict.
func Resolve(query string, results []AgentResult, agentOrder []string, routePrecedence map[string]int) Resolution {
	rank := func(agent string) int {
		for i, a := range agentOrder {
			if a == agent {
				return i
			}
		}
		return len(agentOrder)
	}
	rs := append([]AgentResult(nil), results...)
	sort.SliceStable(rs, func(i, j int) bool {
		if ri, rj := rank(rs[i].Agent), rank(rs[j].Agent); ri != rj {
			return ri < rj
		}
		return rs[i].Agent < rs[j].Agent
	})
	priority := func(channel string) int {
		if p, ok := routePrecedence[channel]; ok {
			return p
		}
		return 1 << 30
	}
	res := Resolution{Query: query}
	byURL := map[string]int{}
	byClaim := map[string]int{}
	agentSeen := map[string]bool{}
	for _, r := range rs {
		if r.Agent != "" && !agentSeen[r.Agent] {
			agentSeen[r.Agent] = true
			res.Agents = append(res.Agents, r.Agent)
		}
		cites := append([]searchdet.Citation(nil), r.Citations...)
		sort.SliceStable(cites, func(i, j int) bool {
			if pi, pj := priority(cites[i].Channel), priority(cites[j].Channel); pi != pj {
				return pi < pj
			}
			if cites[i].AuthorityLevel != cites[j].AuthorityLevel {
				return cites[i].AuthorityLevel > cites[j].AuthorityLevel
			}
			return cites[i].URL < cites[j].URL
		})
		for _, c := range cites {
			url := searchdet.NormalizeURL(c.URL)
			if url == "" {
				continue // nowhere to go: not evidence
			}
			if i, ok := byURL[url]; ok {
				addAgent(&res.Citations[i], r.Agent, c.Channel)
				res.Duplicates++
				continue
			}
			if claim := claimKey(c.Title); claim != "" {
				if i, ok := byClaim[claim]; ok && searchdet.NormalizeURL(res.Citations[i].URL) != url {
					res.Conflicts = append(res.Conflicts, ClaimConflict{
						Claim:  claim,
						Winner: res.Citations[i].URL,
						Losers: []string{c.URL},
						Agents: agentsOf(res.Citations[i].Agents, r.Agent),
					})
					continue
				}
			}
			m := MergedCitation{Citation: c}
			addAgent(&m, r.Agent, c.Channel)
			byURL[url] = len(res.Citations)
			if claim := claimKey(c.Title); claim != "" {
				byClaim[claim] = len(res.Citations)
			}
			res.Citations = append(res.Citations, m)
		}
	}
	if res.Citations == nil {
		res.Citations = []MergedCitation{}
	}
	if res.Conflicts == nil {
		res.Conflicts = []ClaimConflict{}
	}
	return res
}

// addAgent adds the agent and route to a merged citation, once each, sorted.
func addAgent(m *MergedCitation, agent, route string) {
	if agent != "" && !contains(m.Agents, agent) {
		m.Agents = append(m.Agents, agent)
		sort.Strings(m.Agents)
	}
	if route != "" && !contains(m.Routes, route) {
		m.Routes = append(m.Routes, route)
		sort.Strings(m.Routes)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func agentsOf(agents []string, add string) []string {
	out := append([]string(nil), agents...)
	if add != "" && !contains(out, add) {
		out = append(out, add)
		sort.Strings(out)
	}
	return out
}

// claimKey is a claim's identity: its title's words, lowercased and sorted,
// so "Google buys Wiz" and "Wiz bought by Google" are one claim. A title
// with no words has no key.
func claimKey(title string) string {
	words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(words) < 2 {
		return ""
	}
	sort.Strings(words)
	return strings.Join(words, " ")
}
