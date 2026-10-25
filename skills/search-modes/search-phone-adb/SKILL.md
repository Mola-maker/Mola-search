---
name: search-phone-adb
description: "M29 Phone-Use 手机 ADB 真机操控 App 搜索 (Open-AutoGLM / Mobile-Agent-v2 ADB) — 针对仅限手机 App 的封闭场景（大麦 App 实时选座余票、小红书/抖音深层评论、封闭小程序）：通过 Android ADB MCP + AutoGLM-Phone 自动唤醒 App 点击搜索、截屏脱敏并与检索通道交叉核实。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M29 · Phone-Use 手机 ADB 真机操控 App 搜索 (Open-AutoGLM / Mobile-Agent-v2 ADB) (`search-phone-adb`)

> **所属搜索族系**：🗺️🎬🖥️ 地图/音视频/CU操设备/搜索模型族 (`multimodal_cu_geo`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M29`）  
> **目标 P50 延迟**：`~1650 ms`  
> **激活多模态渠道**：`cu_autoglm_adb, astrlink_redact, search_provider`  
> **联动记忆层级**：`L0 -> L1 -> L2 -> L4`

## 1. 何时使用本搜索模式 (When to Activate)
针对仅限手机 App 的封闭场景（大麦 App 实时选座余票、小红书/抖音深层评论、封闭小程序）：通过 Android ADB MCP + AutoGLM-Phone 自动唤醒 App 点击搜索、截屏脱敏并与检索通道交叉核实。

## 2. 底层执行拓扑 (Execution Topology)
```text
Mobile Task -> ADB Wake App (Damai/Xiaohongshu/WeChat) -> AutoGLM SoM Tap & Scroll -> Screencap + Privacy Blur -> Provider Verify
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 2, "Freshness": "d1", "Cnt": 10}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M29">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M29"
  }
}
```
