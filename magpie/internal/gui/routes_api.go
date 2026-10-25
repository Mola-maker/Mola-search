package gui

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/searchdet"
	"github.com/yetone/magpie/internal/searchroutes"
)

// The search-route API: the registry of every reliable path, each one's
// last real request, the per-agent tech stacks, the multi-agent conflict
// resolution (a swarm run), the agent marketplace scan, and scheduled
// searches running on a real ticker.
//
// A run through here is the same engine the Detective page uses: the same
// channels, the same keys, the same measured latencies. A route with no
// keys is reported as not configured, never run and never given a number.

var (
	routesOnce  sync.Once
	routesFlow  *searchroutes.FlowStore
	routesStack *searchroutes.StackStore
	routesSched *searchroutes.Scheduler

	routesMu       sync.Mutex
	routesMarkets  []searchroutes.Market
	routesScanAt   time.Time
	routesStackErr string
	routesSchedErr string
)

// routesScanEvery is how long a marketplace scan is reused. A scan reads
// folders, not the network; this keeps a redrawing page from reading them
// on every request.
const routesScanEvery = 30 * time.Second

// routesInit makes the process's one route state. Loading a file that does
// not parse is kept as an error for the page, never as "no schedules" or
// "no stacks".
func routesInit() {
	routesOnce.Do(func() {
		dir := searchdet.DataDir()
		routesFlow = searchroutes.NewFlowStore()
		routesStack = searchroutes.NewStackStore(filepath.Join(dir, "search_routes.json"))
		if err := routesStack.Load(); err != nil {
			routesStackErr = err.Error()
			log.Printf("search routes: %v", err)
		}
		routesSched = searchroutes.NewScheduler(filepath.Join(dir, "search_schedules.json"), func(ctx context.Context, opt searchdet.Options) searchdet.Investigation {
			return detectiveEngine().Investigate(ctx, opt)
		})
		if err := routesSched.Load(); err != nil {
			routesSchedErr = err.Error()
			log.Printf("scheduled search: %v", err)
		}
	})
}

// startSearchRoutes starts the saved searches' ticker. It is called once by
// the process that serves the page.
func startSearchRoutes(ctx context.Context) {
	routesInit()
	routesSched.Start(ctx)
}

// scanMarkets reads the agent marketplace, reusing a scan this fresh. It
// returns the markets and how long the scan took (the old scan's age when
// it was reused). A file a scan could not read is in the market's Errors,
// not a failed scan.
func scanMarkets(force bool) ([]searchroutes.Market, time.Duration) {
	routesMu.Lock()
	if !force && !routesScanAt.IsZero() && time.Since(routesScanAt) < routesScanEvery {
		m, age := routesMarkets, time.Since(routesScanAt)
		routesMu.Unlock()
		return m, age
	}
	routesMu.Unlock()

	start := time.Now()
	markets := searchroutes.Discover(searchroutes.Targets())
	took := time.Since(start)

	routesMu.Lock()
	routesMarkets, routesScanAt = markets, time.Now()
	routesMu.Unlock()
	return markets, took
}

type staticError struct{ s string }

func (e *staticError) Error() string { return e.s }

// routesSnapshot is the whole page's state from real sources.
type routesSnapshot struct {
	Routes         []searchroutes.Route      `json:"routes"`
	Flows          []searchroutes.Flow       `json:"flows"`
	Stacks         []searchroutes.AgentStack `json:"stacks"`
	Schedules      []searchroutes.Schedule   `json:"schedules"`
	AssetsFromDisk bool                      `json:"assetsFromDisk"`
	ScanAt         string                    `json:"scanAt,omitempty"`
	Errors         map[string]string         `json:"errors,omitempty"`
}

// routeRegistry is the registry as the engine's own gates make it.
func routeRegistry() []searchroutes.Route {
	e := detectiveEngine()
	markets, _ := scanMarkets(false)
	counts := searchroutes.Counts(markets)
	return searchroutes.Registry(searchdet.ResolveKeys(), e.Settings().DisabledChannels, counts)
}

func routesSnapshotOf() routesSnapshot {
	routesInit()
	routes := routeRegistry()
	markets, _ := scanMarkets(false)
	stacks := searchroutes.Derive(searchroutes.Targets(), markets, routes)
	stacks = searchroutes.Merge(routesStack.Stacks(), stacks)
	snap := routesSnapshot{
		Routes: routes, Flows: routesFlow.All(), Stacks: stacks,
		Schedules: routesSched.List(), AssetsFromDisk: assetsFromDisk(),
		Errors: map[string]string{},
	}
	routesMu.Lock()
	if !routesScanAt.IsZero() {
		snap.ScanAt = routesScanAt.Format(time.RFC3339)
	}
	routesMu.Unlock()
	if routesStackErr != "" {
		snap.Errors["stacks"] = routesStackErr
	}
	if routesSchedErr != "" {
		snap.Errors["schedules"] = routesSchedErr
	}
	return snap
}

// routePrecedence is the registry's channel order, for conflict resolution.
func routePrecedence(routes []searchroutes.Route) map[string]int {
	out := map[string]int{}
	for _, r := range routes {
		if r.Channel != "" {
			out[r.Channel] = r.Precedence
		}
	}
	return out
}

// searchRoutes registers the page's endpoints on the window's mux.
func searchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/routes", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, routesSnapshotOf())
	})

	// a real request through one route, timed here
	mux.HandleFunc("POST /api/routes/probe", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		routesInit()
		route, ok := searchroutes.RouteOf(routeRegistry(), in.ID)
		if !ok {
			fail(rw, &staticError{"no search route " + in.ID})
			return
		}
		if route.Kind == searchroutes.KindMarketplace {
			kind := searchroutes.MarketplaceKind(route.ID[len("agent-"):])
			markets, took := scanMarkets(true)
			flow := routesFlow.RecordDiscovery(kind, searchroutes.Items(markets, kind), took, time.Now())
			writeJSON(rw, map[string]any{"flow": flow, "routes": routeRegistry()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		flow, err := routesFlow.ProbeRoute(ctx, route, "")
		writeJSON(rw, map[string]any{"flow": flow, "error": routeErrString(err), "routes": routeRegistry()})
	})

	// the user's own per-agent tech stack
	mux.HandleFunc("POST /api/routes/stack", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Agent  string   `json:"agent"`
			Routes []string `json:"routes"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		routesInit()
		known := map[string]bool{}
		for _, rt := range routeRegistry() {
			known[rt.ID] = true
		}
		if _, err := routesStack.Set(in.Agent, in.Routes, known); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, routesSnapshotOf())
	})

	// a swarm: each agent searches on its own stack, and the topic's pages
	// and claims are resolved once
	mux.HandleFunc("POST /api/routes/swarm", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Query  string   `json:"query"`
			ModeID string   `json:"modeId"`
			Agents []string `json:"agents"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		if routeTrim(in.Query) == "" {
			fail(rw, &staticError{"a swarm needs a query"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		res, err := runSwarm(ctx, in.Query, in.ModeID, in.Agents)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, res)
	})

	// scheduled searches: add, pause, resume, cancel, run now
	mux.HandleFunc("POST /api/routes/schedules", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Action   string `json:"action"`
			ID       string `json:"id"`
			Name     string `json:"name"`
			Query    string `json:"query"`
			ModeID   string `json:"modeId"`
			EverySec int    `json:"everySec"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		routesInit()
		var err error
		switch in.Action {
		case "add":
			_, err = routesSched.Add(searchroutes.Schedule{Name: in.Name, Query: in.Query, ModeID: in.ModeID, EverySec: in.EverySec})
		case "pause":
			err = routesSched.Pause(in.ID)
		case "resume":
			err = routesSched.Resume(in.ID)
		case "cancel":
			err = routesSched.Cancel(in.ID)
		case "run":
			ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
			defer cancel()
			_, err = routesSched.RunNow(ctx, in.ID)
		default:
			fail(rw, &staticError{"no schedule action " + in.Action + " (add, pause, resume, cancel or run)"})
			return
		}
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"schedules": routesSched.List()})
	})
}

// swarmRun is one agent's real run in a swarm.
type swarmRun struct {
	Agent     string               `json:"agent"`
	Routes    []string             `json:"routes"`
	TraceID   string               `json:"traceId,omitempty"`
	Verdict   string               `json:"verdict,omitempty"`
	LatencyMs int                  `json:"latencyMs,omitempty"`
	Citations []searchdet.Citation `json:"citations,omitempty"`
	Error     string               `json:"error,omitempty"`
}

// swarmResult is a swarm's answer: what each agent found, and the topic
// resolved once.
type swarmResult struct {
	Query      string                  `json:"query"`
	Runs       []swarmRun              `json:"runs"`
	Resolution searchroutes.Resolution `json:"resolution"`
	Routes     []searchroutes.Route    `json:"routes"`
}

// runSwarm runs each named agent's stack through the engine, in parallel,
// then resolves the topic once. An agent with no stack or no configured
// channel is reported, not run.
func runSwarm(ctx context.Context, query, modeID string, agents []string) (swarmResult, error) {
	routesInit()
	routes := routeRegistry()
	markets, _ := scanMarkets(false)
	derived := searchroutes.Merge(routesStack.Stacks(), searchroutes.Derive(searchroutes.Targets(), markets, routes))
	stackOf := map[string][]string{}
	for _, s := range derived {
		stackOf[s.Agent] = s.Routes
	}
	if len(agents) == 0 {
		for _, s := range derived {
			if len(s.Routes) > 0 {
				agents = append(agents, s.Agent)
			}
		}
	}
	if len(agents) == 0 {
		return swarmResult{}, &staticError{"no agent has a search stack yet: pick one on the routes page"}
	}
	if len(agents) > 8 {
		agents = agents[:8]
	}
	runs := make([]swarmRun, len(agents))
	var wg sync.WaitGroup
	for i, agent := range agents {
		i, agent := i, agent
		wg.Add(1)
		go func() {
			defer wg.Done()
			stack := stackOf[agent]
			channels := searchroutes.ChannelIDs(routes, stack)
			run := swarmRun{Agent: agent, Routes: stack}
			if len(channels) == 0 {
				run.Error = "no engine route in its stack: the marketplace routes are a scan, not a run"
				runs[i] = run
				return
			}
			inv := detectiveEngine().Investigate(ctx, searchdet.Options{
				Query: query, ModeID: modeID, Origin: "Agent/" + agent, Channels: channels,
			})
			routesFlow.RecordInvestigation(inv)
			run.TraceID, run.Verdict, run.LatencyMs = inv.TraceID, inv.Verdict, inv.TotalLatencyMs
			run.Citations = inv.Citations
			runs[i] = run
		}()
	}
	wg.Wait()

	results := make([]searchroutes.AgentResult, 0, len(runs))
	for _, run := range runs {
		if run.Error != "" {
			continue
		}
		results = append(results, searchroutes.AgentResult{Agent: run.Agent, Query: query, Citations: run.Citations})
	}
	res := searchroutes.Resolve(query, results, agents, routePrecedence(routes))
	return swarmResult{Query: query, Runs: runs, Resolution: res, Routes: routes}, nil
}

// recordRouteFlows puts a real run's measured steps into the route flows
// the Search routes page draws. A Detective run is the same engine, so its
// flow is that route's last real request too.
func recordRouteFlows(inv searchdet.Investigation) {
	routesInit()
	routesFlow.RecordInvestigation(inv)
}

// routeErrString is "" for nil, the error's text otherwise, so a JSON
// answer can carry a real failure beside what did run.
func routeErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func routeTrim(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
