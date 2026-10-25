---
name: search-selfrag-abstain
description: "M11 自反思防幻觉拒答搜索 (Self-RAG Abstain-Aware — 查不到敢说不知道) — 核心防幻觉门控：严格评估相关性 [ISREL] 与证据支持度 [ISSUP]。当检索通道最高权威度 < 2 或全网查无实据时，强制触发 ABSTAIN 拒答印章，绝不瞎编！"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M11 · 自反思防幻觉拒答搜索 (Self-RAG Abstain-Aware — 查不到敢说不知道) (`search-selfrag-abstain`)

> **所属搜索族系**：🔄 自迭代递归与反思族 (`recursive`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M11`）  
> **目标 P50 延迟**：`~360 ms`  
> **激活多模态渠道**：`search_provider, rag_vector`  
> **联动记忆层级**：`L0 -> L2`

## 1. 何时使用本搜索模式 (When to Activate)
核心防幻觉门控：严格评估相关性 [ISREL] 与证据支持度 [ISSUP]。当检索通道最高权威度 < 2 或全网查无实据时，强制触发 ABSTAIN 拒答印章，绝不瞎编！

## 2. 底层执行拓扑 (Execution Topology)
```text
Multi-Source Search -> Check Max(authority_level) & Independent Domains -> [Pass if >= Threshold | Emit ABSTAIN Guardrail if Insufficient]
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 1, "Industry": "gov,acad,news", "Cnt": 15}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M11">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M11"
  }
}
```
