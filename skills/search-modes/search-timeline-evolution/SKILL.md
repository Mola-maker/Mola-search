---
name: search-timeline-evolution
description: "M17 时间线演进与反转重构搜索 (Temporal Tree & Graphiti Evolution) — 阶梯式调用检索通道 Freshness (y1 -> m1 -> d7 -> d1) 分段检索，结合 L2 双时序边自动生成面向人类的『🌲 交互式事件演化时间树』，呈现造谣到官方辟谣反转全过程。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M17 · 时间线演进与反转重构搜索 (Temporal Tree & Graphiti Evolution) (`search-timeline-evolution`)

> **所属搜索族系**：⏳ 时间线与话题垂域族 (`temporal_topic`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M17`）  
> **目标 P50 延迟**：`~590 ms`  
> **激活多模态渠道**：`search_provider, rag_graph, image_reverse`  
> **联动记忆层级**：`L0 -> L2 -> L4`

## 1. 何时使用本搜索模式 (When to Activate)
阶梯式调用检索通道 Freshness (y1 -> m1 -> d7 -> d1) 分段检索，结合 L2 双时序边自动生成面向人类的『🌲 交互式事件演化时间树』，呈现造谣到官方辟谣反转全过程。

## 2. 底层执行拓扑 (Execution Topology)
```text
Multi-Horizon Search (y1/m1/d7/d1) -> Align valid_from/invalid_at -> Build SUPERSEDES Edges -> Render Interactive Temporal Tree
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 0, "Freshness": "y1,m1,d7,d1", "Cnt": 25}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M17">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M17"
  }
}
```
