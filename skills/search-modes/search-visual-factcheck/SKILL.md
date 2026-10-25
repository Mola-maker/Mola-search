---
name: search-visual-factcheck
description: "M22 视觉截图查证搜索 (Aletheia Screenshot & Claim Fact-Check) — 进阶挑战『截图查证』核心模式：VLM 识别聊天记录/网页截图中的核心断言、发言人、UI 伪造痕迹，经隐私脱敏后调WSA SearchPro (含 pics 图文比对) 核实真伪。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M22 · 视觉截图查证搜索 (Aletheia Screenshot & Claim Fact-Check) (`search-visual-factcheck`)

> **所属搜索族系**：🛡️ 隐私、视觉与联邦安全族 (`privacy_multimodal`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M22`）  
> **目标 P50 延迟**：`~690 ms`  
> **激活多模态渠道**：`search_provider, vlm_ocr, image_reverse, astrlink_redact`  
> **联动记忆层级**：`L0 -> L2 -> L4`

## 1. 何时使用本搜索模式 (When to Activate)
进阶挑战『截图查证』核心模式：VLM 识别聊天记录/网页截图中的核心断言、发言人、UI 伪造痕迹，经隐私脱敏后调WSA SearchPro (含 pics 图文比对) 核实真伪。

## 2. 底层执行拓扑 (Execution Topology)
```text
Screenshot Image -> VLM Claim & Tamper Box Extraction -> PII Redact -> Tencent Cloud WSA SearchPro + Pics Match -> Visual Verification Report
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 0, "Industry": "gov,news", "Cnt": 20}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M22">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M22"
  }
}
```
