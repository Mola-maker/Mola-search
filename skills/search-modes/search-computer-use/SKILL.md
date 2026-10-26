---
name: search-computer-use
description: "M28 Computer-Use 浏览器与桌面操设备搜索 (@playwright/mcp a11y + UI-TARS + OmniParser SoM) — 突破无 API 围墙花园：优先用 @playwright/mcp 读取 1.5KB 无障碍语义树 (a11y YAML) 极速点击筛选网页；遇桌面软件（如 PC 微信搜一搜）则唤醒 UI-TARS-1.5 + OmniParser V2 SoM 编号锚框操控并截屏脱敏取证。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M28 · Computer-Use 浏览器与桌面操设备搜索 (@playwright/mcp a11y + UI-TARS + OmniParser SoM) (`search-computer-use`)

> **所属搜索族系**：🗺️🎬🖥️ 地图/音视频/CU操设备/搜索模型族 (`multimodal_cu_geo`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M28`）  
> **目标 P50 延迟**：`~1150 ms`  
> **激活多模态渠道**：`cu_playwright, cu_uitars_desktop, astrlink_redact, search_provider`  
> **联动记忆层级**：`L0 -> L1 -> L4`

## 1. 何时使用本搜索模式 (When to Activate)
突破无 API 围墙花园：优先用 @playwright/mcp 读取 1.5KB 无障碍语义树 (a11y YAML) 极速点击筛选网页；遇桌面软件（如 PC 微信搜一搜）则唤醒 UI-TARS-1.5 + OmniParser V2 SoM 编号锚框操控并截屏脱敏取证。

## 2. 底层执行拓扑 (Execution Topology)
```text
Task -> Tier-2 Playwright a11y Tree Action (<400ms) OR Tier-3 UI-TARS + OmniParser SoM Click -> Screenshot + CU Visual Redact -> Provider Cross-Check
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 2, "Cnt": 10}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M28">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M28"
  }
}
```
