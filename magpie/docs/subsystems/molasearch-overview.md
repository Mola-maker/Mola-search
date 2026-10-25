# MolaSearch: as-built overview

MolaSearch is an **independent search-gateway product built on a fork of
yetone/magpie** (`_ref/magpie` records the upstream base): one binary with
two added nav pages and three new Go packages on top of magpie's shell and
gateway. The search engine and memory store are MolaSearch's own. Today the
window title, data directory and process name still say magpie — closing
that branding gap is a product decision recorded in
[`proposals/目标架构_v6.md`](../../../../../proposals/目标架构_v6.md), not
current behavior. Nothing else in this page describes a future design;
proposals live outside this repository.

## What runs where

```
agent (Claude Code, Codex, …)          browser / desktop window
        │ MCP stdio                              │ http://127.0.0.1:3430
        ▼                                        ▼
molasearch.exe mcp search              molasearch.exe web (magpie web)
        │                                        │  cookie: magpie_web_<port>
        └──────────────┐        ┌────────────────┘
                       ▼        ▼
                internal/gui  →  /api/detective/*, /api/rag/*
                       │
          ┌────────────┴─────────────┐
          ▼                          ▼
 internal/searchdet          internal/ragstore
 (plans, channels,           (six Stores, hybrid
  verdicts, multimodal)       retrieval, hot swap)
          │                          │
          │        internal/usmf     │
          └────────►  record  ◄──────┘
                       │
                internal/sqlitedb, appdir/provider
```

The LLM gateway on `127.0.0.1:3425` is magpie's own; MolaSearch's APIs are on
the `magpie web` port, **3430** by default. They are different contracts and
must not be mixed.

## Packages

| Path | Owns | Does not |
| --- | --- | --- |
| `internal/searchdet` | 30 mode plans, live channels, redaction, ranking, verdicts, temporal tree, multimodal checks, stakeout diffs | HTTP routes, RAG writes, import `ragstore` |
| `internal/ragstore` | six backends behind `Store`, embeddings, BM25 + cosine + RRF, reranking, hot swap, USMF, habits, stakeout rows | HTTP routing, search verdicts |
| `internal/usmf` | the `CanonicalMemoryRecord` both sides exchange | behavior |
| `internal/mcpsearch` | stdio JSON-RPC MCP server: initialize, tools/list, tools/call | anything beyond calling the engine |
| `internal/gui` (`detective_api.go`, `rag_api.go`) | `/api/detective/*`, `/api/rag/*`, persistence, agent injection | evidence computation |
| `plugins/{detective,ragstore}` + `assets/plugins/…` | nav pages and browser code | direct storage or network calls |

The import rule is one-way: `ragstore` → `searchdet` for engine-adjacent
types, `searchdet` never imports `ragstore`, and the shared record lives in
`internal/usmf` so `Store` itself does not depend on the engine
(`internal/ragstore/contract_test.go` guards it).

## Runtime paths

### One investigation

1. `POST /api/detective/investigate` (or MCP `tools/call`) decodes
   `{query, modeId, debate, enableCU, imageBase64, pageUrl}` and calls
   `searchdet.Engine.Investigate` with a 90 s deadline.
2. `redactForInvestigation` loads the compliance policy (built-in list, or
   `MOLA_COMPLIANCE_FILE`) and blocks on a hit. A policy file that cannot be
   read or is empty blocks too: fail closed.
3. `planFor` maps the mode to its sub-queries and switches (HyDE, snowball,
   hops, AREX/RAAC rounds, gate, temporal tree, graph, authority, freshness,
   no-fold, zero retention).
4. Enabled channels run concurrently with a per-channel deadline
   (`channelTimeoutMs`, 12 s default); the user's provider switches and each
   open breaker remove a provider from the allowlist. Each run records its
   own start time, latency, raw hits and error, and `primaryEngine` is
   derived from the providers that actually returned hits.
5. AREX gap rounds and citation snowball run when the mode asks. Results
   merge, rank by authority/freshness, fold near-duplicates (64-bit SimHash,
   Hamming ≤ 6), cap at `topK`, apply the authority/freshness filters, run
   entity hops and the mode's multimodal checks.
6. `assess` derives `VERIFIED / REFUTED / UNVERIFIED / ABSTAIN` from
   coverage, authoritative sources and authority-weighted signals; no
   evidence means `ABSTAIN`, never an invented answer.
7. The GUI persists the run as a USMF record unless `noPersist` is set or the
   run was blocked, then learns habits from the citations.
8. `originAgent` and every trajectory step name the caller: `Detective UI`,
   or `MCP/<clientInfo.name> <version>` from the MCP handshake.

### One memory search

`POST /api/rag/search` → `Manager.Search`: embed the query → active backend
only → dense cosine + BM25 fused by RRF(k=60) → optional remote reranker →
`hits[]` carrying `cosine`, `bm25`, `rrf`, `rerank` and the record.

### One hot swap

`POST /api/rag/hotswap` → `Manager.HotSwap`:

0. Export the corpus to `backup_<source>_<utc>.magpie-rag.jsonl`, verify its
   hash; no backup, no swap.
1. Dual-write every record to the target; the source keeps serving reads.
2. Re-embed every record with the target's embedder.
3. Hash both corpora (`memoryId|claim|evidenceText`, sorted by id); a
   mismatch returns an error and **does not switch**. On a match
   `atomic.Pointer` flips and new writes go to the target.

## State and configuration

| What | Where |
| --- | --- |
| SQLite mirror + FTS5, vectors, habits, stakeout rows | `MOLASEARCH_DATA` (or `<exe>/data`), `molasearch_hybrid.sqlite3` |
| Markdown vault | `<data>/vault/*.md` |
| USMF backups/exports | `<data>/*.magpie-rag.jsonl` |
| Search/vision/embedding keys | environment / `.env` (see `.env.example`) |
| Provider gates and thresholds | `<data>/search_settings.json` (`internal/searchdet/settings.go`) |
| Compliance policy | built-in list, or `MOLA_COMPLIANCE_FILE` (one term per line; empty/unreadable fails closed) |
| Web sign-in | `MAGPIE_WEB_KEY`; cookie `magpie_web_<port>` |

## Failure behavior

- No evidence → `ABSTAIN` with a reason; the page says so.
- A channel fails → its step says so; other channels continue.
- One provider disabled or its breaker open → the remaining providers serve
  the run; every provider disabled → no search runs and the zero-evidence
  path abstains.
- Compliance policy unreadable → no request leaves the machine.
- Remote backend down → status says `不可用`; search answers from the mirror.
- Hot-swap mismatch or a missing backup → fail closed, old backend stays
  active.
- Reranker error → RRF order is kept.
- Image sent to a vision model → EXIF/GPS stripped by re-encoding first.

## Verification

```powershell
cd _ref/magpie
go build ./...
go vet ./internal/searchdet ./internal/ragstore ./internal/mcpsearch ./internal/usmf
go test ./internal/searchdet/ ./internal/ragstore/ ./internal/mcpsearch/ -count=1 -race
cd ../..
python scripts/verify_real_engine.py --exe E:/molasearch/molasearch.exe
```

The end-to-end script starts a fresh `web` process on its own port and
asserts live citations, measured trajectory, SQLite persistence, image
fingerprints, a three-phase hot swap with a SHA-256 and a backup, hybrid
search, USMF export, a stakeout diff and agent injection. Its last recorded
run on this tree is in the commit body of the change that shipped it.

## Known limits

- `postgres_pgvector`, `milvus_zilliz`, `elasticsearch` and `neo4j_graphiti`
  probe their real servers and rank with what those servers return, but
  persist rows in the local SQLite mirror because no drivers are vendored.
- The fusion network opens as a DOM five-column view; the trajectory tab can
  switch to a 2D force-directed canvas where `fusionNetwork.edges` are drawn
  as real co-occurrence edges (adjacent-column links are labelled as layout
  only). There is no 3D renderer.
- The binary, window title and settings text are magpie's. The fork's core
  diff is listed in `PATCHES.md`.
- `proposals/` in the outer repository is not built. `TriAudience`,
  `tokenBudget` and `memory_tier` are proposals, not current API fields.
