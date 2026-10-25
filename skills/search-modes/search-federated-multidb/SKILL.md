---
name: search-federated-multidb
description: "M24 联邦多库与多网关路由融合搜索 (Federated Multi-DB & Peer Gateway) — 跨本地与远端多个热插拔数据库（SQLite + Markdown Vault + PostgreSQL + Milvus + ES + Neo4j）及对等 Magpie 网关节点并发联邦检索，统一归一化为 USMF 格式。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M24 · 联邦多库与多网关路由融合搜索 (Federated Multi-DB & Peer Gateway) (`search-federated-multidb`)

> **所属搜索族系**：🛡️ 隐私、视觉与联邦安全族 (`privacy_multimodal`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M24`）  
> **目标 P50 延迟**：`~380 ms`  
> **激活多模态渠道**：`search_provider, rag_vector, rag_bm25, rag_graph, peer_gateway`  
> **联动记忆层级**：`L1 -> L2 -> L4`

## 1. 何时使用本搜索模式 (When to Activate)
跨本地与远端多个热插拔数据库（SQLite + Markdown Vault + PostgreSQL + Milvus + ES + Neo4j）及对等 Magpie 网关节点并发联邦检索，统一归一化为 USMF 格式。

## 2. 底层执行拓扑 (Execution Topology)
```text
Query -> Fan-out [Active DB + Shadow DB + Peer Magpie Gateways + Provider] -> USMF Normalization -> RRF Fusion
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 0, "Cnt": 15}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M24">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M24"
  }
}
```
