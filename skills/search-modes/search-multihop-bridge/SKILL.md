---
name: search-multihop-bridge
description: "M06 多跳实体桥接搜索 (Multi-Hop Entity Bridge — 寻剧/寻项目神器) — 专为『只记得模糊剧情找剧 / 只记得外号找开源项目』设计：第一跳广撒网提取高频候选桥接实体 E_bridge，第二跳锁定实体精确核验。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M06 · 多跳实体桥接搜索 (Multi-Hop Entity Bridge — 寻剧/寻项目神器) (`search-multihop-bridge`)

> **所属搜索族系**：🧭 启发式与图谱探索族 (`heuristic`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M06`）  
> **目标 P50 延迟**：`~580 ms`  
> **激活多模态渠道**：`search_provider, video_bili_yt, rag_graph`  
> **联动记忆层级**：`L0 -> L1 -> L2`

## 1. 何时使用本搜索模式 (When to Activate)
专为『只记得模糊剧情找剧 / 只记得外号找开源项目』设计：第一跳广撒网提取高频候选桥接实体 E_bridge，第二跳锁定实体精确核验。

## 2. 底层执行拓扑 (Execution Topology)
```text
Hop-1 (Plot/Clue Broad Search) -> Extract Bridge Entities {E1, E2} -> Hop-2 (Entity Attribute Exact Verify) -> Evidence Chain
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 0, "Cnt": 25}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M06">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M06"
  }
}
```
