# MolaSearch memory store (ragstore)

The memory layer behind the Detective page: six backends behind one `Store`
interface, hybrid retrieval with real scores, a three-phase hot swap, USMF
cold export/import, learned habits and persisted stakeout watches. This is
the as-built reference for `internal/ragstore` and `/api/rag/*`.

## Responsibilities and boundaries

| Part | Does | Does not |
| --- | --- | --- |
| `internal/ragstore` | storage, embeddings, BM25 + cosine + RRF, hot swap, USMF, habits, stakeout rows | HTTP routing, the detective engine's verdicts |
| `internal/gui/rag_api.go` | `/api/rag/*` routes, agent-file sync | storage internals |
| `plugins/ragstore` + `assets/plugins/ragstore` | the nav.page and its browser code | direct database access |

It imports `searchdet` only for engine-adjacent helpers (embedding keys,
data dir, citations, stakeout/backend wire types). The record the `Store`
contract exchanges is `internal/usmf.CanonicalMemoryRecord`, in its own
package, so the memory layer does not depend on the retrieval engine for its
core type. `searchdet.CanonicalMemoryRecord` is an alias, so both spellings
are one type.

## Source of truth

- The `Store` contract: `internal/ragstore/types.go`.
- The shared record: `internal/usmf/usmf.go`; `contract_test.go` fails if
  the `Store` interface starts naming `searchdet` again.
- Backend implementations: `sqlite.go`, `vault.go`, `remote.go`.
- The manager's choice state and hot swap: `manager.go`, `hotswap.go`, `status.go`.
- Embedding: `embedder.go`; lexical scoring: `lexical.go`; reranking: `rerank.go`.

## Backends

| id | Implementation | What is real |
| --- | --- | --- |
| `sqlite_hybrid` | `SQLiteHybridStore` | `modernc.org/sqlite`, WAL, `memory_records` / `memory_vectors` / `memory_edges` / `agent_habits` / `stakeout_jobs`, FTS5 when the build has it |
| `markdown_vault` | `MarkdownVaultStore` | one `.md` per fact with YAML frontmatter and `[[WikiLinks]]`, temp-file + rename writes, re-read on `All` |
| `postgres_pgvector` | `PostgresStore` | TCP probe of `MOLA_PG_DSN` (default `127.0.0.1:5432`); rows live in the shared SQLite mirror because no pg driver is vendored |
| `milvus_zilliz` | `MilvusStore` | `GET /healthz` on `MOLA_MILVUS_BASE`; mirror storage |
| `elasticsearch` | `ElasticsearchStore` | `GET /` version probe, PUT into `molasearch_evidence`, cluster BM25 ranking fused with local dense ranking |
| `neo4j_graphiti` | `Neo4jStore` | Cypher `RETURN 1` transaction probe; 1-hop neighbor expansion over `memory_edges` |

A failed probe is reported as `AvgLatency: "不可用"` in `Status`; the search
still answers from the mirror. `recordCount` and the address shown are real.

## Retrieval

`Manager.Search` embeds the query with the configured embedder (remote
`MOLA_EMBED_*` when set, otherwise the local 1024-dim FNV-1a signed feature
hash), asks the active backend, then applies the reranker if one is
configured. The store's own ranking is:

1. dense cosine over stored `float32[1024]` vectors;
2. Okapi BM25 (FTS5 when available and the query is not CJK; the in-process
   index otherwise, because the vendored SQLite has no CJK segmenter);
3. reciprocal rank fusion, `k = 60`;
4. bi-temporal validity (`valid_from` / `invalid_at`) before either ranking.

`Hit` carries `cosine`, `bm25`, `rrf` and, when a remote reranker ran,
`rerank`. A reranker error keeps the RRF order and logs.

## Hot swap (three phases + backup)

`POST /api/rag/hotswap` → `Manager.HotSwap(ctx, targetID, embedder)`:

0. Export the current corpus to `backup_<source>_<utc>.magpie-rag.jsonl` and verify its hash. No backup, no swap.
1. **Dual-write:** every record is written to the target while the source keeps serving. The active pointer is untouched.
2. **Re-embed:** every record's text is embedded with the target's embedder and written again.
3. **Verify + switch:** both corpora are hashed as `memoryId|claim|evidenceText` in id order; on mismatch the swap returns an error, `sqlite_hybrid` stays active, and the source keeps answering. On match `atomic.Pointer` flips.

`SwapReport` records `phase1WriteMs`, `phase2EmbedMs`, `phase3SwitchMs`,
`totalMs`, `sha256`, `backupPath` and a phase log. Reads never take the
manager's write lock: `Active()` reads an `atomic.Pointer`.

## Choices: embedding, reranker, memory mode

- `EmbeddingModel()` is the id of the live embedder. A remote model without `MOLA_EMBED_API_BASE` is refused; there is no silent fallback to another vector space.
- `RerankerModel()` is the configured reranker or `RRF(k=60) 稠密+BM25 融合`. `SetReranker` accepts the RRF name or a model whose endpoint is configured (`MOLA_RERANK_API_BASE/KEY/MODEL`), and rejects a different model id.
- `MemoryMode()` is `persistent` or `ephemeral`. In `ephemeral`, `Insert` writes nothing and `LearnFromInvestigation` learns nothing; reads keep working.

## USMF and habits

`ExportUSMF` writes JSON Lines, one `CanonicalMemoryRecord` per line, sorted
by id, streamed. `ImportUSMF` batches 100 records, skips malformed lines and
reports how many were skipped. A hot-swap backup uses the same format.

`LearnFromInvestigation` collapses the query into a pattern, updates
`ModeAffinity`, and moves domains between `TrustedDomains` and
`BlockedDomains` using the citations' authority levels. Habits are stored in
`agent_habits` on the primary store and served by `GET /api/rag/habits`.

## Failure behavior

- A remote backend down: probe fails, status says `不可用`, searches use the mirror.
- A vault write: temp file + rename; a reader never sees half a note.
- A hot-swap mismatch: fail closed — no switch, source still active.
- A reranker error: keep RRF order.
- An unknown memory mode or embedding model: HTTP error, no state change.

## Verification

```powershell
cd magpie
go test ./internal/ragstore/ -count=1 -race
```

`hotswap_test.go` covers the three properties that would otherwise be claims:
reads keep answering while the target is paused mid-write; a target that lost
a record is refused and the source stays active (with a backup on disk); and
after a swap to the vault new writes become `.md` files that SQLite never
sees. It also covers ephemeral mode writing and learning nothing.
`contract_test.go` guards the direction of the record type: the `Store`
interface speaks `internal/usmf`, not `searchdet`.
