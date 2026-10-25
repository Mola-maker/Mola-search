---
name: search-vertical-authority
description: "M20 权威垂域硬锁定搜索 (Authority-Locked Gov/Acad/Finance Search) — 面向医疗健康、食品安全、法律法规、金融数据核查：硬性锁定检索通道 Mode=1 (仅高权威结果) + Industry=gov|acad|finance，直接丢弃 authority_level < 3 的自媒体。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M20 · 权威垂域硬锁定搜索 (Authority-Locked Gov/Acad/Finance Search) (`search-vertical-authority`)

> **所属搜索族系**：⏳ 时间线与话题垂域族 (`temporal_topic`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M20`）  
> **目标 P50 延迟**：`~290 ms`  
> **激活多模态渠道**：`search_provider`  
> **联动记忆层级**：`L0 -> L2 -> L3`

## 1. 何时使用本搜索模式 (When to Activate)
面向医疗健康、食品安全、法律法规、金融数据核查：硬性锁定检索通道 Mode=1 (仅高权威结果) + Industry=gov|acad|finance，直接丢弃 authority_level < 3 的自媒体。

## 2. 底层执行拓扑 (Execution Topology)
```text
Query -> Provider(Mode=1, Industry=gov|acad|finance) -> Hard Filter(authority_level >= 3) -> Gold-Standard Citation Pack
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 1, "Industry": "gov,acad,finance,news", "Cnt": 15}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M20">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M20"
  }
}
```
