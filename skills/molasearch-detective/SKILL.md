---
name: molasearch-detective
description: "MolaSearch 侦探搜索网关：13 个地位相同的检索 Provider、30 种搜索模式、多源联网查证、判定（含 ABSTAIN）、时间树、多模态取证与调查账本。需要查证、找一手信源或核实网传说法时使用。"
---

# 🔍 MolaSearch 侦探搜索网关

## 核心原则
「少点幻觉，多点搜索；查得到，信得过；查不到敢说不知道。」

1. **没有主通道**。13 个 Provider 地位相同：腾讯云 WSA SearchPro（TC3，可选密钥）、Tavily / Exa / Brave / SearXNG（可选密钥），以及免 Key 的 Bing / DuckDuckGo / You.com / Wikipedia / 百度百科 / GitHub / Bilibili / arXiv。任一通道被停用或熔断，其余通道继续服务；全部不可用才 ABSTAIN。
2. **判定来自证据**。`VERIFIED / REFUTED / UNVERIFIED / ABSTAIN` 由覆盖度、权威源与权威加权的正反信号合成；`ABSTAIN` 就是查不到，必须如实告诉用户，不得凭记忆补答。
3. **引用必须可点开**。每条引用带真实 URL、域名、通道与权威级 0~5；时间树只用抓到的真实日期。
4. **隐私与合规先于网络**。查询先过红线词表与 PII 打码；读不到红线词表时拒绝检索（fail closed）。图片发给远程 Vision 前重编码，EXIF/GPS 不出本机。

## 真实 MCP 工具（只有这四个）
| 工具 | 参数 | 作用 |
|---|---|---|
| `magpie_investigate` | `query`, `modeId`（默认 `M11`） | 完整查证：判定 + 引用 + 时间树 + 轨迹 + 证据信封 |
| `magpie_search_fast` | `query` | M01 直通检索 |
| `magpie_debate` | `query` | M13 正反方对抗 |
| `magpie_timeline` | `query` | M17 时间演化 |

30 种模式**不是 30 个工具**：它们都是 `magpie_investigate` 的 `modeId`。单模式说明里的“工具名”只是模式标识，不是额外的 MCP 工具。

## 30 种模式（M01~M30）
增强族 M01~M04：直通、混合召回、HyDE、多查询改写。
图谱族 M05~M08：缺口分支、实体多跳、共现图、引文雪球。
递归族 M09~M12：AREX 缺口迭代、RAAC 新颖度收敛、Self-RAG 拒答、递归分解。
辩论族 M13~M16：正反对抗、多轮辩论、发散探索、证伪优先。
时间族 M17~M20：时间树、强时效、垂域权威、盯梢巡检。
隐私与多模态族 M21~M30：脱敏零留存、图片取证、代码取证、联邦多库、地理气象、视频、DOM/ADB、模型对照。

每个模式实际读取的开关（`internal/searchdet/modes.go` 的 `planFor`）才是契约；目录文案不描述引擎不执行的行为。

## 输出
- `verdict`: `VERIFIED / REFUTED / UNVERIFIED / ABSTAIN`
- `citations[]`: 真实 URL、域名、通道、权威级、发布时间
- `channelHits`: 本次实际贡献引用的通道
- `trajectory[]`: 每步的真实通道、参数、开始/结束偏移与耗时
- `agentEnvelopeXml`: 给 Agent 的紧凑证据信封

## 设置与门控
所有门控都在 `search_settings.json`，页面 `POST /api/detective/settings` 保存、`POST /api/detective/channels` 启停单个 Provider：TopK、单通道条数、合并池、折叠衰减、通道超时、时效窗口、权威阈值、判定信号数、支撑度下限、熔断阈值/冷却、缺口与新颖度轮次上限。调用/命中数是本进程实测，不伪造厂商额度。

## 诚实边界
- 判定仍是词面启发式（覆盖度 + 权威加权），不是 NLI 模型。
- PostgreSQL / Milvus / Elasticsearch / Neo4j 做真实协议探测/写入，但行数据由本地 SQLite 镜像承载（未 vendor 驱动）。
- 三端差异化编译、token 预算、`memory://` 资源句柄属于路线图，不在当前 API 里。
