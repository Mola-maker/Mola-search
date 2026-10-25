---
name: search-video-keyframe
description: "M27 视频九宫格关键帧与弹幕字幕搜索 (TwelveLabs + Bilibili/yt-dlp + 3x3 Contact Sheet) — 先用 yt-dlp / bilibili-mcp 极速拉取时间轴字幕与高能指路/辟谣弹幕，再用 FFmpeg 抽取镜头切换帧拼合为单张带时间戳的 3x3 九宫格大图（省 85% 视觉 Token），配合 TwelveLabs Marengo 视频向量与检索通道查证。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M27 · 视频九宫格关键帧与弹幕字幕搜索 (TwelveLabs + Bilibili/yt-dlp + 3x3 Contact Sheet) (`search-video-keyframe`)

> **所属搜索族系**：🗺️🎬🖥️ 地图/音视频/CU操设备/搜索模型族 (`multimodal_cu_geo`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M27`）  
> **目标 P50 延迟**：`~890 ms`  
> **激活多模态渠道**：`video_bili_yt, twelvelabs_video, search_provider`  
> **联动记忆层级**：`L0 -> L2 -> L4`

## 1. 何时使用本搜索模式 (When to Activate)
先用 yt-dlp / bilibili-mcp 极速拉取时间轴字幕与高能指路/辟谣弹幕，再用 FFmpeg 抽取镜头切换帧拼合为单张带时间戳的 3x3 九宫格大图（省 85% 视觉 Token），配合 TwelveLabs Marengo 视频向量与检索通道查证。

## 2. 底层执行拓扑 (Execution Topology)
```text
Video URL/File -> Fetch Subtitles & Bilibili Danmaku (<400ms) -> FFmpeg Scene-Cut 3x3 Contact Sheet -> VLM + TwelveLabs + Provider Verify
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 0, "Cnt": 15}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M27">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M27"
  }
}
```
