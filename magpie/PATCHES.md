# MolaSearch patches to magpie

MolaSearch is an **independent search-gateway product built on a fork of
magpie** (proposals/目标架构_v6.md §一). The identity rename landed on
2026-10-24 — window and tray titles, the tray menu, the wordmark and page
title, the CLI banner and its subcommand lines, and the MolaMaker palette;
the section at the end lists exactly what still says magpie. Technical
identifiers are deliberately unchanged: the `magpie://` URL scheme,
`MAGPIE_*` environment names, `~/.config/magpie` paths, plugin ids and the
community's own names. This file is the whole delta against upstream, so a
rebase is a small mechanical patch rather than a hunt through renamed
strings; after the rename, upstream changes to the renamed files are
cherry-picked by value instead.

Base: `b995095` (`gui: a routing group's strategy is switched from its row`).

## Core diff (9 tracked files, 60 inserted lines, 3 removed)

| File | Lines | What and why |
| --- | --- | --- |
| `internal/cli/main.go` | +4 | `magpie mcp search` dispatches to `internal/mcpsearch.Run`; every other `mcp` argument still goes to the image MCP server. |
| `internal/gui/api.go` | +2 | registers `detectiveRoutes(mux)` and `ragStoreRoutes(mux)` on the window's own mux, so the built-in plugins' pages have their API. |
| `plugins/all/all.go` | +2 | blank-imports the two built-in plugin packages (`plugins/detective`, `plugins/ragstore`). |
| `internal/gui/assets/index.html` | +8 | two nav buttons, two stylesheets, four script tags. |
| `internal/gui/assets/start.js` | +1 | with no saved view, the window opens on Detective. |
| `internal/gui/assets/i18n.js` | +32 | 8 keys × 4 languages for the two plugins' Manifest `Name/Sub/One/Many`. `TestBuiltinsTranslated` requires them in core. |
| `internal/gui/builtins_test.go` | +7/−2 | the built-in count is 16: upstream's 14 plus the two MolaSearch pages; and Essentials leaves the two pages off with the other non-essential pages, since that preset keeps the gateway's own pieces. |
| `internal/gui/core_budget_test.go` | +1/−1 | the core script budget records the one line in `start.js` (Detective opens when no page was kept). |
| `docs/subsystems/README.md` | +3 | index rows for the three new references. |

No upstream behavior is changed by these lines; removing them leaves
magpie exactly as it was.

## New files (no upstream conflict surface)

- `internal/searchdet/` — engine: modes, channels, redaction/compliance,
  verdicts, multimodal, stakeout, HyDE, snowball.
- `internal/ragstore/` — six stores, embeddings, hybrid retrieval, rerank,
  hot swap, USMF, habits, stakeout persistence.
- `internal/usmf/` — the shared `CanonicalMemoryRecord`.
- `internal/mcpsearch/` — stdio MCP server.
- `internal/gui/detective_api.go`, `internal/gui/rag_api.go` — routes.
- `plugins/{detective,ragstore}/` — nav.page Manifests.
- `internal/gui/assets/plugins/{detective,ragstore}/` — browser code,
  styles, i18n.
- `docs/subsystems/{molasearch-overview,searchdet,ragstore}.md`.
- Tests beside each package (`compliance_test.go`, `vision_test.go`,
  `hotswap_test.go`, `contract_test.go`, `server_test.go`, …).

## Rebase procedure

1. `git fetch origin && git rebase origin/main` on the MolaSearch branch.
2. If `plugins/all/all.go` changed, keep both import sets: upstream's and
   the two `_ ".../plugins/detective"`, `_ ".../plugins/ragstore"` lines.
3. If `internal/gui/api.go` moved `Handler`, re-add the two route calls
   after `pluginPageRoutes(mux)`.
4. If `internal/cli/main.go`'s `case "mcp"` changed, keep the
   `args[1] == "search"` branch before the `imagemcp.Run` fallback.
5. Re-run `go build ./...`, `go vet`, the package tests and the e2e script;
   `TestBuiltinsTranslated` names any i18n key upstream added a requirement
   for.

## Verification record

For the change that introduced this file:

```text
go build ./...                                                ok
go vet ./internal/searchdet ./internal/ragstore ./internal/mcpsearch ./internal/usmf   ok
go test ./internal/searchdet/ ./internal/mcpsearch/ ./internal/ragstore/ -count=1 -race
                                                              ok (3 packages)
python scripts/verify_real_engine.py --exe E:/molasearch/molasearch.exe
                                                              22/22 checks passed
```

The e2e run used a binary built from this tree, not an earlier one; the log
is in the commit body.

## Not patched

Branding (window title, tray, settings strings), the data directory
(`~/.config/magpie`), and the binary name are upstream's. Changing them
would multiply rebase conflicts for no behavioral gain; the honest
positioning is "MolaSearch: a magpie distribution".

## Privacy and result-quality pass (2026-10-24)

- The output gate now walks every string field of an `Investigation`, not
  only the citations and the summary: claim rows (`status`, `sourceUrl`),
  temporal nodes (`timestamp`, `stage`, `edgeType`), card fields (`icon`,
  `provider`, `status`, `badge`, keyframe `ts`), trajectory
  (`originAgent`, `modeId`, `gateway`, `topDomain`) and the citation's
  `domain`, `channel`, `publishedAt`. `guardOutput` remains independent of
  the query's red-line list, so a result carrying a phone or a key is
  masked even when the query itself was clean.
- `POST /api/detective/upload-image` masks the card it returns with
  `SanitizeCard`; an image card says a GPS tag exists but prints no
  coordinates.
- A saved watch passes the investigate gate: `PrepareWatchQuery` refuses a
  red-line watch (HTTP 400) and masks personal data before the watch is
  stored or searched; `SanitizeWatch` masks legacy rows on the way out and
  every field the page renders. `CheckStakeout` searches the masked query
  and sanitizes what it stores and diffs.
- The Markdown briefing and the agent envelope are built after
  `TotalLatencyMs` is measured, so both quote the run's real latency
  (before, they always said `0 ms` while the page header showed the real
  number). They are masked after being built, because the briefing's title
  carries the user's query.
- Bilibili's HTML fallback reads the real card shape: two anchors per
  video, the title in the `h3`'s `title` attribute (or the cover `alt`),
  protocol-relative hrefs turned absolute, one citation per video. View
  counts and durations no longer become titles. API titles and Wikipedia
  snippets are unescaped after markup is stripped.
- No AstrLink code is vendored. The policy name the page reports is
  `magpie-redact + red-line guard`: the engine's own `internal/redact`
  finders (`Sanitize`, `Options{Secrets: true, Personal: true}`) plus the
  compliance list.

Verification for this pass:

```text
go test ./internal/searchdet/ -count=1 -race                  ok
node internal/gui/tests/i18n-keys.test.cjs                    4/4
go test ./internal/gui/ -run 'Detective|Rag|I18n|Plugin'
  Detective/Rag/I18n pass; six TestPluginPage* fail on Windows because
  symlink creation needs a privilege this account does not hold (environment,
  not this change)
python scripts/verify_real_engine.py --exe E:/molasearch/molasearch.exe
                                                              28/28 checks passed
```

The e2e run added: briefing latency equal to `totalLatencyMs`, a phone
masked before the search and absent from every derived view, a red-line
query blocked with zero trajectory steps, a watch storing the masked query,
and a red-line watch refused with HTTP 400. The live UI run (Playwright,
production build) showed the briefing title `联系 [PHONE] …`, the output
guard line `20/20 · 7 处已替换 · PHONE 7`, no raw phone in the DOM, and
briefing latency 6004 ms equal to the header.

## Tencent WSA SearchPro: the vendor's real contract (2026-10-24)

The channel was calling an API that does not exist. Proven against the live
endpoint with the account's own keys, each wrong shape and the right one:

| Sent | Vendor answer |
| --- | --- |
| `X-TC-Version: 2025-05-22` | `InvalidAction`: "The request action=`SearchPro` is invalid … version=`2025-05-22`." |
| `{"Query","Count","SearchMode"}` on `2025-05-08` | `UnknownParameter`: "The parameter `Count` is not recognized." |
| `{"Query","Mode":2,"Cnt":10}` on `2025-05-08` | 200, `Response.Pages` = 10 JSON-encoded strings |
| MCP `tools/call` with `Bearer <SecretId>:<SecretKey>` | `UnauthorizedOperation` ("未授权操作。") |

What changed:

- Version default `2025-05-08` (`TENCENT_WSA_VERSION` still overrides it).
- Body `{Query, Mode, Cnt}`; `Mode` 2 = 混合结果 (public pages plus
  authoritative vertical answers); `Cnt` snaps to the vendor's
  10/20/30/40/50. `Count` and `SearchMode` are gone.
- `Response.Pages` is an array of JSON-encoded strings, not objects; each
  page is unmarshalled (a bare array or an inlined object is accepted too).
  A page with an empty `url` is a vertical card (weather, gold price) and
  is not a citation. `passage` becomes the snippet (whitespace collapsed,
  300 runes), `site` the source name, `authority_level` the authority when
  the vendor sends it, and `2026/08/10 11:04:00` becomes `2026-08-10`.
- A vendor error is an error (`tencent InvalidAction: …`), not an empty
  result — the trajectory step says why instead of reporting 0 hits.
- The MCP endpoint has its own credential, `TENCENT_WSA_API_KEY` (a WSA API
  key), and runs only when that key is set; `initialize` →
  `notifications/initialized` (202 accepted) → `tools/call` with
  `{Query, Mode, Cnt: "10"}` — the string shape the vendor's own tool
  schema declares — and a reply may be a JSON body or an SSE stream.
  **Not tried live**: this account has no WSA API key; the wire is covered
  by an offline `httptest` server. The settings page lists the key with
  `⭕ 未配置（只走 TC3 签名接口）`, not the keyless fallback wording.
- `KeyStatuses` names the version the channel signs with, so the drawer
  shows what a request will carry.

Verification for this pass:

```text
go test ./internal/searchdet/ -count=1 -race                     ok
gofmt -l internal/searchdet/ ; go vet ./internal/searchdet/      clean
python scripts/verify_real_engine.py --exe E:/molasearch/molasearch.exe
                                                                 32/32
```

The e2e run asks Tencent alone (every other provider disabled, restored
after) and asserts live vendor citations: **10 hits, 8 citations, 1289 ms**
on the signed call. By hand: the version set back to `2025-05-22` turns the
request-shape test red on `X-TC-Version`, and `Cnt` renamed to `Count`
turns it red on the payload; both were restored and the package is green.

## MolaSearch identity and the MolaMaker palette (2026-10-24)

Every surface the goal names now says MolaSearch, in all four languages,
in the MolaMaker colours.

| Surface | File | What changed |
| --- | --- | --- |
| Tray | `internal/gui/app.go` | `SetLabel`/`SetTooltip` `MolaSearch`; the panel's `Title` too. |
| Window | `internal/gui/lightweight.go` | the main window's `Title` is `MolaSearch`. |
| Tray menu | `internal/gui/traymenu.go` (+ test) | open/quit in en, zh, zh-TW, ja, de: 打开 MolaSearch / 開啟 MolaSearch / MolaSearch を開く / MolaSearch öffnen; 退出/結束/終了/beenden. The test's five tuples were updated with the labels. |
| CLI | `internal/cli/main.go` | banner `MolaSearch — one place to pick every agent's model`; every usage line begins `molasearch`; `molasearch: <err>` on stderr; `molasearch <version>`. |
| CLI web/daemon | `internal/cli/web_cli.go`, `daemon_cli.go` | “molasearch web on”, “another MolaSearch”, the LAN warning, and the daemon's “another MolaSearch answers on”. |
| Page | `internal/gui/assets/index.html` | `<title>MolaSearch</title>`; wordmark `Mola<em>Search</em>`; the eight settings strings that named the product. |
| Copy | `internal/gui/assets/i18n.js` | those eight keys and their values in zh, zh-TW, ja, de — and the keep-awake “screen on too” variant, which the first pass missed. |
| Keep-awake line | `internal/gui/assets/app.js` | both `data-en` strings now name MolaSearch. Without this the row fell back to raw English in Chinese, because the key it asked for no longer existed. Two comments were shortened by ten bytes so `TestCoreScriptBudget` stays under its limit instead of the budget being raised. |
| Palette | `internal/gui/assets/app.css` | light: `--mola-ink:#163f70`, `--mola-blue:#246eb6`, `--mola-pink:#d967a7`, `--mola-sky`, `--mola-violet`, `--accent:var(--mola-blue)`, `--accent-soft:#e6f0fc`, `--c1:#246eb6`; dark: `#d5e6fb`, `#7db7ea`, `--accent-soft:#1d3045`, `--c1:#7db7ea`; `.brand-name em` takes the blue. The plugin pages inherit these tokens. |
| Tests | `keep-awake.test.cjs`, `tray-cost.test.cjs`, `tray-bird.test.cjs` | expected words follow the new keys, in each language the test covers. |

Verification for this pass:

```text
go test ./internal/cli/                                           ok (179s)
go test ./internal/gui/ -run 'TestBuiltinsTranslated|TestCoreScriptBudget'
                                                                  PASS
node --test internal/gui/tests/i18n-keys.test.cjs                 4/4 pass
molasearch --help                                                 banner + every line say molasearch
```

Live check on the rebuilt `molasearch.exe web` (Playwright, real page):
title `MolaSearch`; wordmark `<span class="brand-name">Mola<em>Search</em>`;
the `em` computes `rgb(36, 110, 182)` = `#246eb6`; `--accent` is `#246eb6`
in light and `#7db7ea` under `prefers-color-scheme: dark`; at 390 px
`scrollWidth` is 390 (no sideways scroll); Settings in zh reads
“在 MolaSearch 运行任务时，让电脑保持唤醒状态” and its “screen on too”
variant “在 MolaSearch 运行任务时，让电脑和屏幕保持唤醒状态”; switching to
English and back moves the whole shell; zero console errors.

**Not run**: the Node browser suites (`keep-awake`, `tray-cost`,
`tray-bird`) — the `playwright` module is not on this machine's Node path.
Their expected strings were updated and the same rows were checked live in
the page instead. A full `go test ./internal/gui/` run has eight failures
that this change does not touch: six `TestPluginPage*` fail creating a
symlink (`A required privilege is not held by the client`, Windows without
Developer Mode), `TestLodyImportHandoff` reads `%AppData%`, and
`TestUpdateThatWontStart` cannot exec a `.new` file on Windows. They were
red with the rename in place; none reads the strings changed here.

**Still magpie, measured in the running zh page**: Agents 8 visible
strings, Library 1, Routing 4, Gateway 6, Plugins 54 (plugin descriptions
and the community's name). These are product copy, not identity chrome;
the follow-up that rewrites them needs its own pass through `app.js` and
each plugin's `client.js` with the identifiers above left alone.

## G003 — the search and tool plugin platform

A catalog of 39 providers on 9 shelves (web search, embeddings, rerank,
vision, answer models, speech in, speech out, vector stores, agent tools),
each with an icon, tone, docs link, its own key names, connection state and
a real connectivity probe. Keys are written into the same `.env` the engine
already reads; gates are written where the engine already looks for them.

| Area | File | What changed |
| --- | --- | --- |
| Registry | `internal/providerhub/providerhub.go` | 39 providers; shelves; key sets (`Modes`) for Tencent's signed pair or MCP key; slot providers share `MOLA_<SLOT>_*`; keyless ones (DuckDuckGo, Wikipedia, GitHub, Context7…) need no key. |
| State | `internal/providerhub/state.go` | one slot reports one provider: only the active provider reads the slot's values; the others show blank fields rather than borrowing a vendor's key. |
| Saving | `internal/providerhub/envfile.go` | declared fields only, values cannot span lines, atomic 0600 temp-and-rename write, other lines kept; a partial set into a slot is refused before anything is written (`this slot needs every field`); a gate-only save cannot steal a slot. |
| Gates | `internal/providerhub/gates.go` | search providers reuse `searchdet`'s `DisabledChannels`; the other shelves keep `providerhub.json` (`disabled`, `slots`). A corrupt gate file is reported, not swallowed. |
| Probes | `internal/providerhub/probe.go` | real calls per shelf: Tencent search, Exa/Tavily/Brave/Firecrawl, OpenAI-compatible `/models` for LLMs and embedding/rerank/vision, MCP `initialize` for Context7/WSA, TCP for vector stores. A vendor refusal is shown verbatim. |
| API | `internal/gui/providerhub_api.go` | `GET /api/providerhub/catalog`, `POST /api/providerhub/save`, `POST /api/providerhub/test` (409 `not-configured`/`disabled`; 15 s probe); 64 KB body cap. |
| Page | `plugins/providerhub/`, `internal/gui/assets/plugins/providerhub/` | the page registers itself (`page` slot), shelves with counts, per-card state/chips, masked key form, clear-key, gate switch, test result, filter, zh/zh-TW/ja/de plugin dictionary, MolaMaker palette, reduced-motion and `calm()` fallbacks. |
| Shell | `internal/gui/assets/index.html`, `internal/gui/builtins_test.go`, `plugins/all/all.go` | nav entry “Tools & Providers”; built-in count 17; plugin loaded. |
| Tests | `internal/providerhub/providerhub_test.go`, `internal/gui/providerhub_api_test.go` | catalog completeness, key masking, `.env` merge/clear, foreign names refused, gates write both stores, slot truth, partial-slot refusal, probe auth/path/refusals, MCP serverInfo. |

### Icon and Windows identity

The app icon is the MolaMaker character tile from the user's sheet, cropped
at native 268 px with a rounded-rect alpha mask and resized to every size
the build uses: `internal/gui/icon.png` (64), `icon-256.png`,
`icon-1024.png`, `build/windows/icon-{16,32,48,64,128,256}.png`, and the
page's `assets/icons/app-icon.png` (index.html's favicon). `winres.json` now
says `com.molamaker.molasearch`, ProductName/FileDescription `MolaSearch`,
CompanyName `molamaker`; the rebuilt exe reports `ProductName=MolaSearch`,
`FileDescription=MolaSearch`, `CompanyName=molamaker`, `FileVersion=5.0.0.0`.

### Run separation and local Postgres

`scripts/start-web.ps1` now fixes the web key (`data/webkey.txt`), puts the
gateway on `127.0.0.1:3432` (the user's own magpie keeps 3425), and runs the
app with `USERPROFILE`/`HOME` under `<repo>/agent-home` so it cannot
read or rewrite the user's `~/.claude`, `~/.codex`, `~/.dsh` and other agent
configs. `MOLA_PG_DSN` points at a local Postgres container
(`127.0.0.1:15432/molasearch`); the RAG store's `postgres_pgvector` backend
probes it with a real pgx round-trip, and a hot-swap from `sqlite_hybrid`
recorded its phases and backup path (`backup_sqlite_hybrid_…jsonl`).

### Verification

```text
go test ./internal/providerhub ./internal/searchdet                    ok
go test ./internal/gui -run 'TestBuiltins|TestCoreScriptBudget|TestProviderHub|TestStaticAssets'   ok (9.9 s)
node --test internal/gui/tests/i18n-keys.test.cjs                    4/4 pass
go build ./... ; go vet (providerhub, gui, searchdet) ; gofmt -l      clean
```

Live, on the rebuilt exe (Playwright + curl):

- Provider page, zh: 39 cards / 10 tabs, all four languages, light and
  dark, 390 px with no sideways scroll; modal Escape/backdrop; clear-key
  round-trip; filter and empty state.
- Real probes with real keys: Tencent `899 毫秒内有应答` (and the API
  probe reported `10 citations came back`, 1118 ms); DeepSeek
  `451 毫秒内有应答` (`2 entries visible to this key`, 1138 ms).
- Slot truth: saving a complete OpenAI embedding set switched
  `slots.embedding` to `openai-embed`; Jina and Ollama then read
  `Not configured` with blank fields; a partial save was refused with
  “这个槽位需要填写所有字段。” and wrote nothing.
- Detective, real run: “2026年诺贝尔化学奖授予了哪些科学家”, M13 debate,
  36 real requests, 20 citations (14 authoritative), 13/13 channels,
  Tencent WSA SearchPro among them, 6507 ms, verdict UNVERIFIED, privacy
  gate PASS. Trace saved under `demo/2026-10-25/`.
