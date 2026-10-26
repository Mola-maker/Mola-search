---
name: search-citation-snowball
description: "M08 引文溯源顺藤摸瓜搜索 (Citation Chain / Primary Source Snowball) — 从二手新闻/网帖中提取『据XX通报/论文/仓库显示』的一手线索，第二轮强制使用检索通道 Site=一手官网域名 直捣最原始出处。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M08 · 引文溯源顺藤摸瓜搜索 (Citation Chain / Primary Source Snowball) (`search-citation-snowball`)

> **所属搜索族系**：🧭 启发式与图谱探索族 (`heuristic`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M08`）  
> **目标 P50 延迟**：`~540 ms`  
> **激活多模态渠道**：`search_provider, github_aihot`  
> **联动记忆层级**：`L0 -> L2 -> L4`

## 1. 何时使用本搜索模式 (When to Activate)
从二手新闻/网帖中提取『据XX通报/论文/仓库显示』的一手线索，第二轮强制使用检索通道 Site=一手官网域名 直捣最原始出处。

## 2. 底层执行拓扑 (Execution Topology)
```text
Secondary Report -> Regex/AST Extract Primary Source Clue -> Provider(Site=PrimaryDomain, Mode=1) -> Primary Source Anchor
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 1, "Site": "gov.cn", "Cnt": 10}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M08">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M08"
  }
}
```
