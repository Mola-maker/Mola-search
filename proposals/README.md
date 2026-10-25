# Proposals — not the as-built system

Everything in this folder is a **design proposal**. None of it is a
description of what the code does today. The as-built references are in
[`magpie/docs/subsystems/`](../magpie/docs/subsystems/):

- [overview](../magpie/docs/subsystems/molasearch-overview.md)
- [searchdet](../magpie/docs/subsystems/searchdet.md)
- [ragstore](../magpie/docs/subsystems/ragstore.md)

Files here:

- `全栈架构选型与性能优化设计蓝图.md` — the v3.5 blueprint. It is useful as a
  roadmap; several sections describe APIs and structures that do not exist.
- `目标架构_v6.md` — the re-established architecture (2026-10-24): MolaSearch
  as a **standalone, independent search-gateway product** built on a fork of
  magpie (upstream code base, not identity), the five architecture laws,
  seven target subsystems (provider registry, per-channel circuit breakers
  and fail-over, search routing gateway, investigation session ledger,
  migration/restore/cloud-sync visualization, usage ledger, visual token
  layer) and the confirmed known gaps. **Where v6 conflicts with the v3.5
  blueprint, v6 wins.**
- `搜索基准测试方案.md` — the bench plan for the five focus scenarios
  (device-control search, the 30-mode pipeline, strategy hot-plug and the
  RAG backend matrix, video frame extraction, everything-is-a-plugin
  conformance), each mapped to established benchmarks with a replay/live
  two-tier protocol.
- `30模式算法实现与搜索插件集成.md` — making the 30 modes real (the five
  dead switches get implementations sized ~800 lines reusing existing
  interfaces), a three-carrier plugin contract (builtin Go / MCP adapter /
  one manifest), integration of the official ecosystem plugins (exa
  MCP server, tavily MCP, github-mcp-server, brave, searxng) with their env
  registration, registry-generated agent tools, and per-family mini
  topology animations driven by the real `modePlan` data.

## Blueprint claims and their status

| Blueprint claim | Today |
| --- | --- |
| Three-phase hot/cold swap with re-embedding and hash verification | **Built.** `internal/ragstore/hotswap.go`; pinned by `hotswap_test.go`. |
| Six selectable backends | **Built with a limit.** SQLite and the Markdown vault store real data; Postgres, Milvus, Elasticsearch and Neo4j speak real protocols but keep rows in the local SQLite mirror (no vendored drivers). |
| 30 search modes with different topologies | **Built.** `planFor` in `internal/searchdet/modes.go`; the catalogue's topologies are pinned by tests. |
| Live trajectory with measured timings | **Built.** Each channel's start, latency, hits and errors are recorded. |
| Five-column content fusion network | **Built as a DOM layout, plus a canvas 2D force-directed view.** No 3D renderer. |
| Multimodal checks (image, video, DOM, ADB, weather) | **Built.** Remote vision gets re-encoded pixels, never the original EXIF. |
| `TriAudienceCompiler` (`audience`: human/agent/developer), `tokenBudget`, `agentXmlEnvelope` budget enforcement | **Not built.** The API has no `audience` or `tokenBudget` field. `agentEnvelopeXml` exists as a string, with no token cap. |
| Five-tier memory `L0–L4` (`memory_tier`) | **Not built as tiers.** There are `persistent` and `ephemeral` modes, habits, and USMF records; no `memory_tier` parameter. |
| Custom tool specializer and a Discover plugin market | **Not built.** |
| DSH plugin seams / `dsh-plugin-molasearch` runtime integration | **Packaging only.** `plugins-pack/` holds the manifest and entry point; the gateway does not load DSH plugins. |
| 24 modes | **Superseded:** the catalogue serves 30 modes. |

When a proposal is implemented, move its description into the owning
as-built reference and delete the row here.
