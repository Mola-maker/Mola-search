package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// resetRouteState makes the route singletons read this test's folders: the
// process would otherwise keep the first test's data directory.
func resetRouteState(t *testing.T) {
	t.Helper()
	if routesSched != nil {
		routesSched.Stop()
	}
	routesOnce = sync.Once{}
	routesFlow, routesStack, routesSched = nil, nil, nil
	routesMu.Lock()
	routesMarkets, routesScanAt, routesStackErr, routesSchedErr = nil, time.Time{}, "", ""
	routesMu.Unlock()
}

func routeHome(t *testing.T) http.Handler {
	h := builtinHome(t)
	t.Setenv("MOLASEARCH_DATA", t.TempDir())
	resetRouteState(t)
	return h
}

func getRoutes(t *testing.T, h http.Handler) routesSnapshot {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/routes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/routes: %d %s", rec.Code, rec.Body)
	}
	var out routesSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func postRoutes(h http.Handler, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
	return rec
}

// TestRoutesRegistryNamesTheRealPaths: the registry is the engine's
// channels plus this machine's marketplace routes, and a route's readiness
// is its own keys, not a template.
func TestRoutesRegistryNamesTheRealPaths(t *testing.T) {
	h := routeHome(t)
	snap := getRoutes(t, h)
	byID := map[string]bool{}
	for _, r := range snap.Routes {
		byID[r.ID] = true
	}
	for _, id := range []string{"github", "exa", "tavily", "tencent", "agent-skills", "agent-mcp", "agent-plugins", "agent-scripts"} {
		if !byID[id] {
			t.Errorf("no route %q", id)
		}
	}
	if snap.AssetsFromDisk != assetsFromDisk() {
		t.Errorf("asset source %v, want %v", snap.AssetsFromDisk, assetsFromDisk())
	}
}

// TestRouteStackSavesOnlyRealRoutes: the user's stack is persisted, and a
// route id the registry doesn't have is refused.
func TestRouteStackSavesOnlyRealRoutes(t *testing.T) {
	h := routeHome(t)
	if rec := postRoutes(h, "/api/routes/stack", `{"agent":"claude","routes":["github"]}`); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	snap := getRoutes(t, h)
	found := false
	for _, s := range snap.Stacks {
		if s.Agent == "claude" {
			found = s.Source == "user" && len(s.Routes) == 1 && s.Routes[0] == "github"
		}
	}
	if !found {
		t.Errorf("the saved stack: %+v", snap.Stacks)
	}
	if rec := postRoutes(h, "/api/routes/stack", `{"agent":"claude","routes":["nowhere"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown route: %d %s", rec.Code, rec.Body)
	}
}

// TestSchedulesAddPauseResumeCancel: the schedule API persists the saved
// search and its state, and cancel takes it away. Nothing is run here: the
// ticker is the backend's, and the run itself is searchroutes' test.
func TestSchedulesAddPauseResumeCancel(t *testing.T) {
	h := routeHome(t)
	rec := postRoutes(h, "/api/routes/schedules", `{"action":"add","query":"wiz","modeId":"M11","everySec":60}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Schedules []struct {
			ID       string
			Paused   bool
			EverySec int
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Schedules) != 1 || out.Schedules[0].ID == "" || out.Schedules[0].EverySec != 60 {
		t.Fatalf("after add: %+v", out.Schedules)
	}
	id := out.Schedules[0].ID
	for _, step := range []struct {
		action string
		paused bool
	}{{"pause", true}, {"resume", false}} {
		rec := postRoutes(h, "/api/routes/schedules", `{"action":"`+step.action+`","id":"`+id+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", step.action, rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Schedules) != 1 || out.Schedules[0].Paused != step.paused {
			t.Errorf("after %s: %+v", step.action, out.Schedules)
		}
	}
	rec = postRoutes(h, "/api/routes/schedules", `{"action":"cancel","id":"`+id+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Schedules) != 0 {
		t.Errorf("after cancel: %+v", out.Schedules)
	}
	if rec := postRoutes(h, "/api/routes/schedules", `{"action":"pause","id":"none"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown schedule: %d", rec.Code)
	}
}

// TestProbeOfAMarketplaceRouteScansThisComputer: a discovery route's probe
// is a real scan of the folders, not a network call.
func TestProbeOfAMarketplaceRouteScansThisComputer(t *testing.T) {
	h := routeHome(t)
	rec := postRoutes(h, "/api/routes/probe", `{"id":"agent-skills"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("probe: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Flow struct {
			RouteID string
			Source  string
			State   string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Flow.RouteID != "agent-skills" || out.Flow.Source != "discovery" {
		t.Errorf("the scan's flow: %+v", out.Flow)
	}
}
