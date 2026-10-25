---
name: search-code-forensics
description: "M23 代码与开源生态取证搜索 (Code, GitHub Issues & MCP Forensics) — 专为极客与架构师设计：并发调用检索通道检索技术深度评测/踩坑贴 + GitHub API 核验真实源码实现、Issues 报错、Release 变更与 Star 曲线。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M23 · 代码与开源生态取证搜索 (Code, GitHub Issues & MCP Forensics) (`search-code-forensics`)

> **所属搜索族系**：🛡️ 隐私、视觉与联邦安全族 (`privacy_multimodal`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M23`）  
> **目标 P50 延迟**：`~440 ms`  
> **激活多模态渠道**：`search_provider, github_aihot, rag_bm25`  
> **联动记忆层级**：`L0 -> L2 -> L3 -> L4`

## 1. 何时使用本搜索模式 (When to Activate)
专为极客与架构师设计：并发调用检索通道检索技术深度评测/踩坑贴 + GitHub API 核验真实源码实现、Issues 报错、Release 变更与 Star 曲线。

## 2. 底层执行拓扑 (Execution Topology)
```text
Tech Query -> [Provider Tech Articles || GitHub Repo/Code/Issues API || Local Code RAG] -> Cross-Verify Implementation vs Claim
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 0, "Cnt": 15}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M23">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M23"
  }
}
```
