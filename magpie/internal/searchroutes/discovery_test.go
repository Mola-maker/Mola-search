package searchroutes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/searchdet"
)

// TestDiscoverReadsTheRealFolders: a scan lists the skills, MCP servers,
// plugins and scripts that are on disk, and an unreadable file is an error
// rather than an empty list.
func TestDiscoverReadsTheRealFolders(t *testing.T) {
	dir := t.TempDir()
	skills := filepath.Join(dir, "claude", "skills")
	if err := os.MkdirAll(filepath.Join(skills, "web-search", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(skills, "web-search", "SKILL.md"), []byte("---\nname: web-search\n---\n"), 0o644)
	os.WriteFile(filepath.Join(skills, "web-search", "scripts", "search.sh"), []byte("#!/bin/sh\n"), 0o644)
	os.MkdirAll(filepath.Join(skills, "empty-skill"), 0o755)
	mcp := filepath.Join(dir, "claude", "mcp.json")
	os.WriteFile(mcp, []byte(`{"mcpServers":{"molasearch":{"command":"molasearch"},"context7":{}}}`), 0o644)
	bad := filepath.Join(dir, "claude", "bad.json")
	os.WriteFile(bad, []byte("["), 0o644)

	targets := []AgentTarget{{
		Agent: "claude", Name: "Claude Code", Skills: skills,
		MCPFiles: []string{mcp, bad}, Plugins: []string{"@magpie/x"},
	}}
	markets := Discover(targets)
	if len(markets) != 1 {
		t.Fatalf("markets: %+v", markets)
	}
	m := markets[0]
	if len(m.Skills) != 2 || m.Skills[0] != "empty-skill" || m.Skills[1] != "web-search" {
		t.Errorf("skills: %v", m.Skills)
	}
	if len(m.MCP) != 2 || m.MCP[0] != "context7" || m.MCP[1] != "molasearch" {
		t.Errorf("mcp: %v", m.MCP)
	}
	if len(m.Scripts) != 1 || m.Scripts[0] != "web-search/search.sh" {
		t.Errorf("scripts: %v", m.Scripts)
	}
	if len(m.Plugins) != 1 || m.Plugins[0] != "@magpie/x" {
		t.Errorf("plugins: %v", m.Plugins)
	}
	if len(m.Errors) != 1 {
		t.Errorf("an unreadable MCP file is an error: %v", m.Errors)
	}
	counts := Counts(markets)
	if counts[MarketSkills] != 2 || counts[MarketMCP] != 2 || counts[MarketScripts] != 1 || counts[MarketPlugins] != 1 {
		t.Errorf("counts: %v", counts)
	}
	if items := Items(markets, MarketMCP); len(items) != 2 || items[0] != "context7" {
		t.Errorf("items: %v", items)
	}
}

// TestDeriveAndMergeStacks: an agent with MolaSearch's skill and MCP server
// derives the engine's web routes plus the marketplace routes it has, and
// the user's own choice wins over the derivation.
func TestDeriveAndMergeStacks(t *testing.T) {
	routes := Registry(searchdet.Keys{}, nil, map[MarketplaceKind]int{})
	targets := []AgentTarget{{Agent: "claude", Name: "Claude Code", Skills: "C:/skills", MCPFiles: []string{"C:/mcp.json"}}}
	markets := []Market{{
		Agent: "claude", Skills: []string{"molasearch-detective"},
		MCP: []string{"molasearch"}, Plugins: []string{"@x/y"},
	}}
	derived := Derive(targets, markets, routes)
	if len(derived) != 1 {
		t.Fatalf("derived: %+v", derived)
	}
	d := derived[0]
	if d.Source != "derived" || !contains(d.Routes, "github") || !contains(d.Routes, "agent-skills") || !contains(d.Routes, "agent-mcp") || !contains(d.Routes, "agent-plugins") {
		t.Errorf("the derived stack: %+v", d)
	}
	if len(d.Evidence) == 0 {
		t.Error("a derived stack says where it saw MolaSearch")
	}
	merged := Merge(map[string][]string{
		"claude": {"github"},
		"gone":   {"tavily"},
	}, derived)
	if len(merged) != 2 {
		t.Fatalf("merged: %+v", merged)
	}
	for _, s := range merged {
		switch s.Agent {
		case "claude":
			if s.Source != "user" || len(s.Routes) != 1 || s.Routes[0] != "github" {
				t.Errorf("the user's own stack wins: %+v", s)
			}
		case "gone":
			if s.Source != "user" || len(s.Routes) != 1 || s.Routes[0] != "tavily" {
				t.Errorf("an agent only the user names: %+v", s)
			}
		}
	}
}

// TestStackStoreSavesOnlyKnownRoutes: a typo is refused, and what is saved
// is read back from the file.
func TestStackStoreSavesOnlyKnownRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search_routes.json")
	store := NewStackStore(path)
	known := map[string]bool{"github": true, "tavily": true, "agent-skills": true}
	if _, err := store.Set("claude", []string{"github", "nope"}, known); err == nil {
		t.Fatal("an unknown route was saved")
	}
	got, err := store.Set("claude", []string{"tavily", "github", "github"}, known)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Routes) != 2 || got.Source != "user" {
		t.Errorf("saved: %+v", got)
	}
	again := NewStackStore(path)
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	if s := again.Stacks(); len(s["claude"]) != 2 {
		t.Errorf("after a reload: %+v", s)
	}
	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := again.Load(); err == nil {
		t.Error("a broken file loaded as no choices")
	}
}
