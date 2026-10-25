# MolaSearch search routes

Every reliable way a search can reach the web or this computer, what each
one really needs, the last request that went through it, each agent's own
stack, what several agents searching one topic come to, and the searches
that run on their own interval. This is the as-built reference for
`internal/searchroutes`, the `/api/routes/*` endpoints, and the Search
routes page (`plugins/routes`, `internal/gui/assets/plugins/routes/`).

Nothing here is invented. A route with no keys is `configured: false`; a
flow is built from a real run's own measured steps; a scan lists what is on
disk; a conflict is a claim that two agents' results actually placed at
different pages.

## Responsibilities and boundaries

| Part | Does | Does not |
| --- | --- | --- |
| `internal/searchroutes` | the route registry, flow store, marketplace scan, per-agent stacks, conflict resolution, the schedule store and ticker | run a search itself (the engine does), write an agent's files |
| `internal/gui/routes_api.go` | `/api/routes`, `/api/routes/probe`, `/api/routes/stack`, `/api/routes/swarm`, `/api/routes/schedules`; starts the ticker with the backend | compute a verdict or a conflict |
| `plugins/routes` + its assets | the page: routes and their flows, stacks, swarm, schedules, hot-plug | network calls of its own |
| `internal/searchdet` | the channels, the investigation, the trajectory the flows are read from | the registry or the stacks |

The engine stays the source of truth for what a channel is: a route is a
`searchdet.Channel` plus the provider platform's key names. `Options.Channels`
is how a stack bounds a run to its own providers.

## Source of truth

- Channels and readiness: `searchdet.ChannelInfos` (`internal/searchdet/investigate.go`).
- Provider names, docs and key names: `providerhub.All()` with `Category == search`.
- A run's measured steps: `Investigation.Trajectory`, each step carrying
  `ChannelID`, `StartOffsetMs`, `DurationMs` and `RawHits`.
- Saved stacks: `searchdet.DataDir()/search_routes.json` (only the user's own
  choices; the rest is derived).
- Schedules: `searchdet.DataDir()/search_schedules.json`.

## The registry

`Registry(keys, disabled, found)` is every engine channel in the engine's
order (GitHub repository search, Exa, Tavily, Tencent/Yuanbao SearchPro,
Brave, SearXNG, DuckDuckGo, Wikipedia, Bing, You.com, Baidu Baike, Bilibili,
arXiv), named by the provider platform when it knows the channel, then the
four marketplace routes: `agent-skills`, `agent-mcp`, `agent-plugins`,
`agent-scripts`. `configured` is the channel's own `Enabled(keys)` (or a
scan's count for a discovery route); `enabled` is the user's per-channel
gate. `precedence` is the engine's order, and conflict resolution uses it.

## Flows

`FlowStore` keeps the last flow of each route in memory: after a restart a
route says "not run yet", which is true. A flow is filled from

- `RecordInvestigation`: a finished engine run's trajectory, grouped by
  `ChannelID`, one step per real request with that step's offset,
  milliseconds and hits;
- `ProbeRoute`: one route asked for real by the page's Test button, timed
  here, including a configured check that fails (`ErrChannelNotConfigured`);
- `RecordDiscovery`: a real scan's measured time and item count.

The Detective page's runs are recorded too (`recordRouteFlows`), since they
are the same engine. The page draws a rail whose ticks sit at the steps'
real offsets and whose fill takes the measured duration; `calm()` and
`prefers-reduced-motion` show it complete instead of moving it.

## Per-agent stacks

`searchroutes.Targets()` reads the agents `library.Targets()` finds: their
skills folder and MCP files, plus MolaSearch's installed community plugins.
`Discover` lists the skills (folders), MCP server names (JSON keys
`mcpServers`, `mcp`, `mcp_servers`), plugins (the plugin store's specs) and
scripts (files under a skill's `scripts/`). An unreadable file is an error
on that market, never "no servers".

`Derive` gives an agent the engine's web routes when its own folders show
MolaSearch wired (the `molasearch-detective` skill or an MCP server named
`molasearch`), and the marketplace routes it has items for. `StackStore`
holds the user's own choice; `Merge` lays it over the derivation (source
`user`). A stack the user set survives an agent that is not installed now.

## Conflict resolution

`Resolve(query, results, agentOrder, routePrecedence)` merges several
agents' citations deterministically: agents in the order given (then by
id), each agent's citations by route precedence, authority, then URL. A
page two agents found is one citation carrying both (`Duplicates`); a claim
two agents placed at different pages keeps the higher-precedence page and
records a `ClaimConflict` with the loser. The claim key is its title's
words, lowercased and sorted.

The swarm endpoint runs each selected agent's stack through the engine in
parallel (`Options.Channels`), records the flows, then resolves the topic
once. An agent with no engine route is reported, not run.

## Scheduled search

`Scheduler` persists `Schedule` records (query, mode, interval, paused, the
last run's trace id, verdict, latency, hits and error) and runs a real
ticker (`Start`). A due, unpaused schedule runs `Engine.Investigate` through
the runner the GUI supplies, in its own goroutine; `Pause` stops the next
run and cancels one in flight, `Resume` sets the next interval, `Cancel`
removes it, and `RunNow` runs it whatever the interval says. The first run
is one interval after `Add`, never immediately.

## API

| Route | Body / answer |
| --- | --- |
| `GET /api/routes` | `{routes, flows, stacks, schedules, assetsFromDisk, scanAt, errors}` |
| `POST /api/routes/probe` | `{id}` → `{flow, error, routes}`; a discovery route re-scans |
| `POST /api/routes/stack` | `{agent, routes}` → the whole state; an unknown route id is refused |
| `POST /api/routes/swarm` | `{query, modeId, agents}` → `{runs, resolution, routes}` |
| `POST /api/routes/schedules` | `{action: add\|pause\|resume\|cancel\|run, ...}` → `{schedules}` |

## Failure behavior

- A marketplace scan is reused for 30 seconds; a forced scan is the probe.
- A file that cannot be read or parsed is an error and keeps what was
  loaded before (stacks and schedules both fail closed).
- A route with no keys reports `configured: false` and is never run.
- A swarm or a schedule with no engine route reports why instead of
  inventing an answer.
- The scheduler is the backend's: it starts in `startBackend`, and a page
  read alone does not run anything.

## Verification

```sh
go test ./internal/searchroutes/ ./internal/pluginrt/ ./internal/searchdet/
go test ./internal/gui/ -run 'Hotplug|Routes|Schedules|ProbeOfA'
go build ./...
```
