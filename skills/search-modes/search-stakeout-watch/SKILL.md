---
name: search-stakeout-watch
description: "M19 持续盯梢增量差分搜索 (Continuous Stakeout / Freshness=d1 Watch) — 进阶挑战核心模式：定时调用检索通道 Freshness=d1 检索 24h 增量网页，与 L2 知识库历史快照做 SimHash 与立场差分，发现结论反转或放票立即告警！"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M19 · 持续盯梢增量差分搜索 (Continuous Stakeout / Freshness=d1 Watch) (`search-stakeout-watch`)

> **所属搜索族系**：⏳ 时间线与话题垂域族 (`temporal_topic`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M19`）  
> **目标 P50 延迟**：`~310 ms`  
> **激活多模态渠道**：`search_provider, cu_playwright, rag_graph`  
> **联动记忆层级**：`L1 -> L2 -> L4`

## 1. 何时使用本搜索模式 (When to Activate)
进阶挑战核心模式：定时调用检索通道 Freshness=d1 检索 24h 增量网页，与 L2 知识库历史快照做 SimHash 与立场差分，发现结论反转或放票立即告警！

## 2. 底层执行拓扑 (Execution Topology)
```text
Cron / Manual Trigger -> Provider(Freshness=d1) -> Diff against L2 Snapshot -> [Update invalid_at & Emit DELTA_REVERSAL_ALERT if Changed]
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 2, "Freshness": "d1", "Cnt": 15}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M19">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M19"
  }
}
```
