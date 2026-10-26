---
name: search-jury-consensus
description: "M14 多源陪审团交叉共识搜索 (Multi-Channel Jury & AIHOT Anti-Spam) — 同时扇出至 腾讯检索通道、GitHub/AIHOT、Tavily/Exa 与本地 RAG 四大独立渠道，施加 AIHOT 域名多样性指数衰减 (γ=0.35) 折叠同源营销号洗稿。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M14 · 多源陪审团交叉共识搜索 (Multi-Channel Jury & AIHOT Anti-Spam) (`search-jury-consensus`)

> **所属搜索族系**：⚔️ 多Agent辩论与创造族 (`debate_creative`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M14`）  
> **目标 P50 延迟**：`~490 ms`  
> **激活多模态渠道**：`search_provider, github_aihot, tavily_exa, rag_vector`  
> **联动记忆层级**：`L0 -> L2 -> L3`

## 1. 何时使用本搜索模式 (When to Activate)
同时扇出至 腾讯检索通道、GitHub/AIHOT、Tavily/Exa 与本地 RAG 四大独立渠道，施加 AIHOT 域名多样性指数衰减 (γ=0.35) 折叠同源营销号洗稿。

## 2. 底层执行拓扑 (Execution Topology)
```text
4-Channel Parallel Fan-out -> Domain Grouping -> Apply Decay γ^(i-1) (γ=0.35) -> Collapse Content Farms -> Cross-Channel Consensus Score
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 0, "Cnt": 25}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M14">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M14"
  }
}
```
