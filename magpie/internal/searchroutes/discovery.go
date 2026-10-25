package searchroutes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/library"
	"github.com/yetone/magpie/internal/plugin"
)

// maxItems caps what one scan lists, so a folder with thousands of files
// cannot make the page unusable. The count says when it was capped.
const maxItems = 500

// AgentTarget is one agent whose folders a scan reads. It is the input
// Discover takes, so a test reads a temp folder instead of this computer.
type AgentTarget struct {
	Agent string // the agent's id, as magpie knows it
	Name  string // its name, as the page says it
	// Skills is the folder the agent finds skills in, "" for none.
	Skills string
	// MCPFiles are the agent's MCP config files, in the order magpie
	// writes them.
	MCPFiles []string
	// Plugins are the community plugins this target has installed (the
	// plugin store's specs), for the MolaSearch target.
	Plugins []string
}

// Market is what one scan found for one agent: the real names, no counts
// invented.
type Market struct {
	Agent  string   `json:"agent"`
	Name   string   `json:"name"`
	Skills []string `json:"skills"`
	MCP    []string `json:"mcp"`
	// Plugins are community plugin specs, and Scripts the executable files
	// a skill carries under scripts/.
	Plugins   []string `json:"plugins"`
	Scripts   []string `json:"scripts"`
	ScannedAt string   `json:"scannedAt"`
	// Errors says which file could not be read: a scan that could not read
	// something says so rather than reporting it empty.
	Errors []string `json:"errors,omitempty"`
}

// Count is how many items of one kind the scan found.
func (m Market) Count(k MarketplaceKind) int { return len(m.Items(k)) }

// Items are the names of one kind.
func (m Market) Items(k MarketplaceKind) []string {
	switch k {
	case MarketSkills:
		return m.Skills
	case MarketMCP:
		return m.MCP
	case MarketPlugins:
		return m.Plugins
	case MarketScripts:
		return m.Scripts
	}
	return nil
}

// Discover reads every target's folders. It writes nothing. A folder that
// is not there is no items, not an error; a file that is there and cannot
// be read is an error on that market, so an unreadable config never reads
// as "no servers".
func Discover(targets []AgentTarget) []Market {
	now := time.Now().Format(time.RFC3339)
	out := make([]Market, 0, len(targets))
	for _, t := range targets {
		m := Market{Agent: t.Agent, Name: t.Name, ScannedAt: now}
		if t.Skills != "" {
			names, err := skillDirs(t.Skills)
			if err != nil {
				m.Errors = append(m.Errors, err.Error())
			}
			m.Skills = names
			m.Scripts = scriptFiles(t.Skills, names)
		}
		if len(t.MCPFiles) > 0 {
			names, errs := mcpServerNames(t.MCPFiles)
			m.MCP, m.Errors = names, append(m.Errors, errs...)
		}
		m.Plugins = append([]string(nil), t.Plugins...)
		sort.Strings(m.Plugins)
		out = append(out, m)
	}
	return out
}

// Counts is how many items of each kind a scan of every agent found.
func Counts(markets []Market) map[MarketplaceKind]int {
	seen := map[MarketplaceKind]map[string]bool{}
	for _, k := range MarketplaceKinds {
		seen[k] = map[string]bool{}
	}
	for _, m := range markets {
		for _, k := range MarketplaceKinds {
			for _, item := range m.Items(k) {
				seen[k][item] = true
			}
		}
	}
	out := make(map[MarketplaceKind]int, len(seen))
	for k, set := range seen {
		out[k] = len(set)
	}
	return out
}

// Items is every distinct name of one kind across the markets, sorted.
func Items(markets []Market, k MarketplaceKind) []string {
	set := map[string]bool{}
	for _, m := range markets {
		for _, item := range m.Items(k) {
			set[item] = true
		}
	}
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

// skillDirs lists the skill folders directly under dir: a folder with a
// SKILL.md, or any folder (some agents accept a skill without one, and
// hiding it would be a guess).
func skillDirs(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if len(out) >= maxItems {
			break
		}
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// scriptFiles lists the files under each skill's scripts folder: the
// algorithms a skill carries and runs.
func scriptFiles(dir string, skills []string) []string {
	var out []string
	for _, s := range skills {
		ents, err := os.ReadDir(filepath.Join(dir, s, "scripts"))
		if err != nil {
			continue // no scripts folder is no scripts
		}
		for _, e := range ents {
			if len(out) >= maxItems {
				break
			}
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			out = append(out, s+"/"+e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// mcpServerNames reads the server names out of MCP config files. A file
// that is not JSON is an error, never "no servers".
func mcpServerNames(files []string) ([]string, []string) {
	var names, errs []string
	seen := map[string]bool{}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		var root map[string]any
		if err := json.Unmarshal(b, &root); err != nil {
			errs = append(errs, path+": not JSON")
			continue
		}
		for _, key := range []string{"mcpServers", "mcp", "mcp_servers"} {
			servers, ok := root[key].(map[string]any)
			if !ok {
				continue
			}
			for name := range servers {
				if !seen[name] && len(names) < maxItems {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	sort.Strings(names)
	return names, errs
}

// Targets are the agents installed on this computer, as library.Targets
// reads them, plus MolaSearch's own installed community plugins. It is the
// one function here that reads the real machine; Discover over the result
// is pure.
func Targets() []AgentTarget {
	var out []AgentTarget
	for _, t := range library.Targets() {
		if t.Agent == nil {
			continue
		}
		tg := AgentTarget{Agent: t.Agent.ID, Name: t.Agent.Name, Skills: t.Skills}
		if t.MCP != nil {
			tg.MCPFiles = append([]string{t.MCP.Path}, t.MCP.Also...)
		}
		out = append(out, tg)
	}
	out = append(out, AgentTarget{Agent: "molasearch", Name: "MolaSearch", Plugins: InstalledPlugins()})
	return out
}

// InstalledPlugins are the community plugins this magpie has installed
// (plugins.json), by spec.
func InstalledPlugins() []string {
	var out []string
	for _, e := range plugin.Load().Plugins {
		out = append(out, e.Spec)
	}
	sort.Strings(out)
	return out
}
