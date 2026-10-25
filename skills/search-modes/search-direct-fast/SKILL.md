---
name: search-direct-fast
description: "M01 直通极速搜索 (Direct Fast Search) — L1 指纹缓存 (<1ms) 短路 + 腾讯云 WSA SearchPro（可选通道） (Mode=2 精确摘要) 单轮极速检索，适用于高频事实核对与低延迟问答。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M01 · 直通极速搜索 (Direct Fast Search) (`search-direct-fast`)

> **所属搜索族系**：⚡ 增强式与混合搜索族 (`augmented`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M01`）  
> **目标 P50 延迟**：`~180 ms`  
> **激活多模态渠道**：`search_provider, l1_cache`  
> **联动记忆层级**：`L0 -> L1`

## 1. 何时使用本搜索模式 (When to Activate)
L1 指纹缓存 (<1ms) 短路 + 腾讯云 WSA SearchPro（可选通道） (Mode=2 精确摘要) 单轮极速检索，适用于高频事实核对与低延迟问答。

## 2. 底层执行拓扑 (Execution Topology)
```text
Query -> Privacy Redact -> L1 Hash Cache -> Tencent Cloud WSA SearchPro(Mode=2) -> Tier-0 Envelope
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 2, "Cnt": 10, "Freshness": "m1"}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M01">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M01"
  }
}
```
