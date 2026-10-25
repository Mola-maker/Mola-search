# MolaSearch search engine (searchdet)

MolaSearch's investigation engine: one query in, live multi-source evidence,
a verdict, or an honest `ABSTAIN`. This page is the as-built reference for
`internal/searchdet`, the `/api/detective/*` routes and the `mcp search`
server. Nothing here is scripted data: every citation comes from a network
answer or the run says it found none.

## Responsibilities and boundaries

| Part | Does | Does not |
| --- | --- | --- |
| `internal/searchdet` | mode plans, live channels, redaction, ranking, folding, verdicts, temporal tree, multimodal checks, stakeout diffs, MCP tool payloads | write to the RAG store, register HTTP routes, import `internal/ragstore` |
| `internal/gui/detective_api.go` | `/api/detective/*`, persistence of a run, stakeout storage calls, agent skill/MCP injection | compute evidence |
| `internal/mcpsearch` | stdio JSON-RPC MCP server (`molasearch.exe mcp search`); it records the client's `clientInfo` from `initialize` and passes it as the run's origin | anything beyond calling `searchdet.Engine` |
| `plugins/detective` + `assets/plugins/detective` | the nav.page and its browser code | network calls of its own |

The import direction is one-way: `ragstore` imports `searchdet`, never the
reverse. That is why stakeout storage calls and memory writes live in the GUI
layer.

## Source of truth

- Mode names, families, topologies and modality lists: `internal/searchdet/modes_catalog.json`.
- What a mode *does*: `planFor` in `internal/searchdet/modes.go` — the only place a mode's pipeline is decided.
- Wire shapes: `internal/searchdet/contract.go` (`Investigation`, `Citation`, `StakeoutJob`, `CanonicalMemoryRecord`, `DBBackendInfo`).
- Key resolution: `internal/searchdet/env.go` (`ResolveKeys`, `KeyStatuses`, `DataDir`).
- The compliance gate: `internal/searchdet/compliance.go` — the built-in
  red-line list, `MOLA_COMPLIANCE_FILE` (one term per line), the masking
  patterns, and the fail-closed policy loader.
- Every gate and threshold: `internal/searchdet/settings.go`
  (`search_settings.json` under `DataDir()`), validated on load and save.
- The post-rank filters: `internal/searchdet/filters.go` (`authFilter`,
  `freshFilter`).
- The shared record type: `internal/usmf.CanonicalMemoryRecord` (aliased in
  `contract.go`).

## Provider registry and gates

Every search source is a `Channel` (`internal/searchdet/channels.go`) with
the same standing. There is no privileged vendor: the engine names the
providers that answered (`primaryEngine`), and no provider is required for a
run to proceed.

| Knob (`search_settings.json`) | Default | What it changes |
| --- | --- | --- |
| `topK` | 20 | citations a verdict may see |
| `perChannel` | 8 | results one channel returns per query |
| `mergeCap` | 40 | deduplicated pool before ranking |
| `foldGamma` | 0.35 | how fast age discounts near-duplicates |
| `channelTimeoutMs` | 12000 | one channel's network deadline |
| `freshDays` | 30 | `FreshOnly` window (undated results kept) |
| `authorityHigh` / `authorityLow` | 4 / 1 | authority thresholds for verdicts and the boost filter |
| `verdictMinSignals` | 2 | weighted refute/confirm signals a verdict needs |
| `supportFloor` | 0.08 | minimum query-token coverage |
| `breakerFails` / `breakerCooldownSec` | 3 / 600 | per-channel circuit breaker |
| `gapRoundsCap` / `noveltyRoundsCap` | 3 / 3 | ceilings over a mode's AREX/RAAC rounds |
| `disabledChannels` | `[]` | providers the user turned off |

The page reads and writes these through `GET/POST /api/detective/settings`
and toggles one provider through `POST /api/detective/channels`. The file is
written temp-and-rename, and a save that fails validation is refused, not
clamped. A key the file leaves out takes its default; a key it names is
applied as written, so a saved `0` (folding off, no gap or novelty rounds)
stays `0` across a reload. The page sends the whole record when it saves, so
provider switches survive a gate edit and a gate reset.

## The 30 modes

`Catalog()` serves the catalog unchanged; `planFor(modeID, query)` maps each
mode to a `modePlan` with real switches: sub-query set, hop count, AREX gap
rounds, RAAC novelty stop, Self-RAG gate, temporal tree, graph edges,
authority boost, freshness, no-fold, zero retention, HyDE and citation
snowball, plus multimodal cards. `TestEveryModePlansDifferentWork` fails when
two modes plan identical work; `TestModePlansMatchTheCatalogTopologies` pins
the modes whose algorithm the catalog names. Separate tests hold each
formerly-dead switch to an observable effect: `TestGapRoundsAskWhatWasNotAnswered`,
`TestNoveltyStopEndsHopWhenNothingNew`, `TestGraphEdgesOnlyWhenModeAsks`.

| Mode | Algorithm the plan runs |
| --- | --- |
| M01 | one query, redact, rank |
| M02 | sparse + dense query variants, fused |
| M03 | HyDE draft (`DEEPSEEK_API_KEY`) searched as a query |
| M04 | four rewritten variants |
| M05 | three scored branches + one gap round + authority boost |
| M06 | entity hops ×2 |
| M07 | dual-level graph queries |
| M08 | citation snowball over the first round's domains |
| M09 | AREX gap rounds ×2 |
| M10 | RAAC novelty stop over two entity rounds |
| M11 | Self-RAG gate |
| M12 | recursive decomposition + hop |
| M13 | pro/con counter-queries + gate + SimHash fold |
| M14 | jury fan-out, no fold, authority boost |
| M15 | analogy + first-principles queries |
| M16 | Popper falsification (counter-queries) |
| M17 | temporal tree |
| M18 | topic-cluster queries, no fold |
| M19 | freshness-first + temporal tree |
| M20 | authority / vertical queries |
| M21 | privacy, zero retention |
| M22 | image card + gate |
| M23 | GitHub/issue code-forensics queries |
| M24 | federated fan-out, no fold, graph |
| M25 | weather card + authority |
| M26 | image provenance + earliest-source query |
| M27 | video card |
| M28 | DOM accessibility tree |
| M29 | DOM tree + ADB |
| M30 | model comparison, no fold |

## Runtime path of one investigation

1. `POST /api/detective/investigate` decodes `{query, modeId, debate, enableCU, imageBase64, pageUrl}`, or MCP `tools/call` arrives; either way `Engine.Investigate` runs with a 90 s deadline. The caller is carried as `Options.Origin` (`Detective UI`, or `MCP/<clientInfo.name> <version>` from the handshake) and appears as `originAgent` on the result and every trajectory step.
2. `redactForInvestigation` loads the compliance policy and masks ID cards, phone numbers, API keys, emails and IPv4 addresses. A red-line hit blocks; so does an unreadable or empty `MOLA_COMPLIANCE_FILE` (fail closed).
3. `planFor` produces the sub-queries and the mode's switches; HyDE and pro/con append theirs.
4. The provider allowlist is the caller's list minus the user's disabled providers and minus any provider whose breaker is open. Every remaining channel runs concurrently with `channelTimeoutMs` (12 s default); each run records name, start, latency, hit count and error. `primaryEngine` is then derived from the runs that actually returned hits.
5. AREX gap rounds (when the mode asks and `gapRoundsCap` allows) re-ask the query terms no citation spoke to; a round that adds no new URL stops the loop.
6. Snowball (M08) asks the first round's domains for primary coverage.
7. Results are merged, ranked with the mode's authority/freshness weights, near-duplicates folded (64-bit SimHash, Hamming ≤ 6), capped at `topK`. `AuthBoost` drops low-authority results only when something else remains; `FreshOnly` drops dated results outside `freshDays`, keeping undated ones.
8. Entity hops run (stopping on novelty or `noveltyRoundsCap`), then the output gate's first half (`guardEvidence`) runs on the evidence: a citation whose title, snippet or URL hits a red-line term is dropped whole, and personal data and secrets in every kept field become `[KIND]` markers (no placeholder, no restore mapping). It runs before the verdict so a masked or dropped result cannot move a claim, a trust score or a multimodal check.
9. The mode's multimodal checks run on the guarded citations, then `assess` derives `VERIFIED / REFUTED / UNVERIFIED / ABSTAIN` from token coverage, authoritative-source count and authority-weighted refute/confirm signals. A gate with no authoritative source and no substance abstains.
10. The verdict's views are built (claim matrix, temporal tree, debate, fusion network, executive summary), then the gate's second half (`guardOutput`) walks every derived string — claim rows, tree nodes, debate lines, trajectory steps, fusion rows and edges, cards including OCR/keyframe text, the abstain reason. An uploaded image's card never prints EXIF GPS coordinates; it says the tag exists. The card returned by `POST /api/detective/upload-image` is masked with the same finders, and a saved watch passes the same input gate (`PrepareWatchQuery`): a red-line watch is refused, a phone is masked before it is stored or searched, and rows written before the gate are masked on the way out.
11. The run's latency is measured, then the Markdown briefing and the agent envelope are built and masked — they quote that latency, so they are built after it is known rather than saying `0 ms`.
12. The GUI persists the run through `ragstore.Manager` unless `noPersist` is set or the run was blocked, then learns habits from the citations.

## Channels

Thirteen providers, every one optional and switchable: keyed — Tencent Cloud
WSA SearchPro (`TENCENT_SECRET_ID`/`TENCENT_SECRET_KEY`, TC3; plus
`TENCENT_WSA_API_KEY` for the vendor's MCP endpoint), Tavily, Exa, Brave,
SearXNG; keyless — Bing, DuckDuckGo, You.com, Wikipedia, Baidu Baike (via
Bing site search), GitHub (anonymous 60 requests/hour), Bilibili, arXiv.
With no keys the keyless set carries the run. The provider id is `tencent`;
no provider is primary.

Tencent's SearchPro wire is the vendor's own contract, confirmed against the
live endpoint on 2026-10-24: `X-TC-Version: 2025-05-08` (`2025-05-22`
answers `InvalidAction`; `TENCENT_WSA_VERSION` overrides), body
`{Query, Mode, Cnt}` (`Count`/`SearchMode` answer `UnknownParameter`),
`Mode` 2 = 混合结果, `Cnt` one of 10/20/30/40/50. `Response.Pages` is an
array of JSON-encoded strings; each is unmarshalled, `passage` becomes the
snippet, `site` the source name, `authority_level` the authority when sent,
and a page with an empty `url` (a weather/gold vertical card) is not a
citation. The vendor's `Response.Error` is returned as an error. The MCP
endpoint (`api.wsa.cloud.tencent.com/Mcp`) takes a WSA API key, not the
SecretId/SecretKey pair, so it runs only with `TENCENT_WSA_API_KEY`; it is
covered by an offline `httptest` server and has not been tried live.

Bilibili asks its search API first. Its HTML fallback reads the result cards
as they really are: one video is two anchors with the same href, the cover
first (whose text is the view count and duration), the title second in an
`h3`'s `title` attribute; hrefs are protocol-relative (`//www.bilibili.com/…`).
The card's name comes from a `title` attribute or a cover `alt`, never from
the cover's stats text, and one card yields one citation. Wikipedia snippets
and API titles are unescaped after their markup is stripped, so `&amp;` and
`&#39;` reach the page as themselves.

A channel that fails is reported as failed and never fails the investigation.
After `breakerFails` consecutive errors its breaker opens for
`breakerCooldownSec`, the provider leaves the allowlist, and the rest keep
serving; when the cooldown passes it is tried again. Turning a provider off
is the same path with no cooldown: one provider leaves, the run continues.

## Failure behavior

- No evidence: `ABSTAIN`, never a made-up answer.
- A channel fails: its trajectory step says so; other channels continue.
- Tencent answers a vendor error (`InvalidAction`, `UnknownParameter`,
  `UnauthorizedOperation`): the step records that error; the channel does
  not report it as "0 hits".
- One provider disabled, or its breaker open: the remaining providers serve
  the run. `ABSTAIN` follows only if nothing came back.
- Every provider disabled: no search runs, and the zero-evidence path
  abstains.
- A red-line hit or an unreadable compliance policy: no request leaves the machine. The output side is independent of that gate: every returned citation and every view built from it is dropped or masked, whether or not the query itself carried a red-line term.
- An uploaded image sent to a vision model is re-encoded from decoded pixels first, so EXIF/GPS and the camera's other fields stay local; a format with no safe encoder is not sent.
- An uploaded image's card says a GPS tag exists but never prints its coordinates; the upload endpoint masks the card's text with the same finders as an investigation.
- A saved watch with a red-line query is refused (HTTP 400) and stores nothing; a phone in a watch query is masked before the watch is stored or searched.
- Multimodal check fails: logged, the text evidence stands.
- A stakeout check whose channels all failed returns an error and stores no snapshot (an empty snapshot is not "no change").

## Verification

```powershell
cd _ref/magpie
go test ./internal/searchdet/ ./internal/mcpsearch/ -count=1 -race
python ../scripts/verify_real_engine.py --exe ../molasearch.exe
```

The unit tests never reach the network (`channelRunner`, `keysFn` and `nowFn`
are injectable). `compliance_test.go` pins every built-in red-line term, a
spaced/punctuated variant, the configured file replacing the list, and the
fail-closed path where an unreadable or empty policy stops the investigation
before any channel runs. `outputguard_test.go` holds the two-half gate to
every string field of an `Investigation` (citations, claim matrix, temporal
tree, debate, trajectory, fusion rows/edges, briefing, envelope) and to the
upload card. `vision_test.go` builds a JPEG with an APP1 GPS segment and
checks both the strip and the data URI handed to the HTTP layer; a structured
TIFF fixture in `stakeout_test.go` checks the card prints no coordinates.
`channels_keyless_test.go` parses a real Bilibili card shape (two anchors,
protocol-relative href, entities) without the network. `tencent_test.go`
signs a SearchPro request against a local server and recomputes the TC3
signature from the bytes it received; it parses a real 200 reply saved in
`testdata/tencent_searchpro.json` (ten JSON-encoded `Pages` strings), holds
the two vendor error replies (`InvalidAction`, `UnknownParameter`) to an
error, and drives the MCP wire (`initialize` → 202 notification →
`tools/call`, string `Cnt`, SSE reply) offline. `stakeout_test.go`
checks a watch query is masked and a red-line watch is refused.
`internal/mcpsearch/server_test.go` drives the real stdio loop and checks
that `initialize`'s `clientInfo` becomes `originAgent`. The end-to-end script
starts `molasearch.exe web`, runs an unseen query, and asserts real citation
URLs, measured trajectory steps, briefing latency equal to the header,
input/output privacy behavior, SQLite persistence, image fingerprints, hot
swap, search, USMF export, stakeout privacy and agent injection.

## Known limitations (confirmed 2026-10-24)

- `assess` is still lexical: it weights "辟谣/谣言/不实" and "证实/通报/官方"
  terms by source authority, and it now requires an authoritative source for
  `VERIFIED`/`REFUTED`, but it is not an NLI model. A page that quotes a
  denial to argue the opposite can still move the signal. The trust score
  (0.6×coverage + 0.4×authority ratio) is a heuristic, not a calibration.
- Settings and breaker state are process-scoped. The web process loads
  `search_settings.json` at start and applies changes immediately; an
  already-running `mcp search` process keeps the gates it loaded until it
  restarts (the file is shared, the in-memory copy is not).
- `Catalog()`/`ModeOf()` re-parse the embedded JSON on every call.
- The fusion network opens as a DOM five-column view; the trajectory tab can
  switch to a 2D force-directed canvas. Real `fusionNetwork.edges` are drawn
  as co-occurrence edges; adjacent-column links are labelled layout only.
  There is no 3D renderer.
- Gap terms are CJK bigrams and Latin words; a short question can leave
  overlapping bigrams that keep one extra round alive until the URL-novelty
  stop ends it.
- AuthBoost/FreshOnly drop only what they can judge: low authority levels,
  and results that carry a date. Undated results are always kept.
