---
name: search-recursive-arex
description: "M09 自迭代递归审计搜索 (AREX Recursive Claim-Audit Search) — 内环迭代收集证据；外环将草稿拆解为原子断言，对无证据支撑或孤证断言自动追加检索通道 Site=exclude 排他反查，结案后自动回写知识库。"
---

> **契约**：本模式由 `magpie_investigate` 以 `modeId` 调用；三端编译、token 预算与 `memory://` 资源句柄是路线图，不在当前 API 里。

# M09 · 自迭代递归审计搜索 (AREX Recursive Claim-Audit Search) (`search-recursive-arex`)

> **所属搜索族系**：🔄 自迭代递归与反思族 (`recursive`)  
> **MCP 调用**：`magpie_investigate`（`modeId: M09`）  
> **目标 P50 延迟**：`~720 ms`  
> **激活多模态渠道**：`search_provider, rag_vector, rag_graph`  
> **联动记忆层级**：`L0 -> L1 -> L2 -> L4`

## 1. 何时使用本搜索模式 (When to Activate)
内环迭代收集证据；外环将草稿拆解为原子断言，对无证据支撑或孤证断言自动追加检索通道 Site=exclude 排他反查，结案后自动回写知识库。

## 2. 底层执行拓扑 (Execution Topology)
```text
Inner Loop (Gather) -> Draft Claims -> Outer Loop (Audit Unsupported Claims -> Provider Site=exclude Follow-up) -> KB Self-Iteration Writeback
```

## 3. 腾讯云 WSA SearchPro（可选通道） 与多模态参数特化模板
```json
{"Mode": 2, "Site": "exclude:baijiahao.baidu.com|sohu.com", "Cnt": 20}
```

## 4. 三端自适应返回（路线图，未实现）
- **面向 Agent (`Audience=agent`)**：返回 `<magpie_evidence_envelope mode="M09">` 紧凑 XML 信封（`< 800 tokens`），自动附带该 Agent 的 `L3 搜索习惯提示` 与可按需深拉全文的 MCP Resource 句柄（`memory://trace/{trace_id}/{ev_id}/full`）。
- **面向人类 (`Audience=human`)**：编译为结构化 Markdown 调查简报 + 🌲 交互式事件演化时间树 (`Temporal Tree`) + 可点击一手来源胶囊 `[[1]](url)`。
- **防幻觉红线**：若通道 `authority_level` 与交叉渠道证据不足，必须触发 `ABSTAIN（查不到敢说不知道）`，严禁编造事实或链接；严禁触碰时政与个人隐私信息。

## 5. 调用示例 (MCP / SDK)
```json
{
  "tool": "magpie_investigate",
  "arguments": {
    "query": "待核查的问题、地点、截图或视频线索",
    "modeId": "M09"
  }
}
```
