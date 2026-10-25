---
name: search-model-ensemble
description: "M30 多搜索大模型交叉 Grounding 蒸馏搜索 (Provider + Perplexity Sonar + Gemini + OpenAI) — 复用 Magpie 多模型路由层，同时向 腾讯混元/检索通道 DeepSearch、Perplexity Sonar/Agent API、Gemini 2.5 Search Grounding、OpenAI web_search_preview 扇出提问，提取高重合度内联引用蒸馏零幻觉报告。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M30 · 多搜索大模型交叉 Grounding 蒸馏搜索 (Provider + Perplexity Sonar + Gemini + OpenAI) (`search-model-ensemble`)

> **所属搜索族系**：🗺️🎬🖥️ 地图/音视频/CU操设备/搜索模型族 (`multimodal_cu_geo`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M30`）  
> **目标 P50 延迟**：`~780 ms`  
> **激活多模态渠道**：`search_provider, search_llm`  
> **联动记忆层级**：`L0 -> L2 -> L3`

## 1. 何时使用本搜索模式 (When to Activate)
复用 Magpie 多模型路由层，同时向 腾讯混元/检索通道 DeepSearch、Perplexity Sonar/Agent API、Gemini 2.5 Search Grounding、OpenAI web_search_preview 扇出提问，提取高重合度内联引用蒸馏零幻觉报告。

## 2. 底层执行拓扑 (Execution Topology)
```text
Query -> Fan-out [Provider DeepSearch || Perplexity Sonar || Gemini Grounding || OpenAI WebSearch] -> Citation Overlap Distillation
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 2, "Cnt": 15}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M30">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M30"
  }
}
```
