# MolaSearch 实测痕迹 · 2026-10-25

本目录是当天真实运行留下的证据。所有数字来自本机实际请求，没有估算。
密钥不在仓库里：应用从仓库根目录的 `.env`（已忽略）读取。

## 运行环境

| 项 | 值 |
| --- | --- |
| 可执行文件 | `molasearch.exe`（ProductName `MolaSearch`，CompanyName `molamaker`，版本 5.0） |
| 网页 / 网关 | `http://127.0.0.1:3430` / `127.0.0.1:3432`（用户自己的 magpie 保留 3425） |
| 搜索主通道 | 腾讯云 WSA SearchPro（元宝搜索），真实 SecretId/SecretKey |
| 其他通道 | Bing、DuckDuckGo、Wikipedia、哔哩哔哩、You.com（免费）等，共 13 个在线通道 |
| 回答模型 | DeepSeek（供应商平台真实连通测试） |
| RAG 主库 | 本地 Postgres `127.0.0.1:15432/molasearch`（`postgres_pgvector`，已热切换） |
| 隐私门 | 输入侧红线校验；输出侧 `magpie-redact + red-line guard` |

## 真实检索

问题：**2026年诺贝尔化学奖授予了哪些科学家**

| 模式 | 判定 | 可信分 | 真实请求 | 引用 | 权威引用 | 端到端 |
| --- | --- | --- | --- | --- | --- | --- |
| M13 多 Agent 正反方对抗 | UNVERIFIED | 100 | 36 | 20 | 14 | 6507 ms |
| M11 自反思拒答 | UNVERIFIED | 100 | 9 | 20 | 14 | 2778 ms |

- 证据来源包括中新网、人民网、新华网等，答案指向 Henri B. Kagan 与 Kenso Soai（2026-10-07 公布）。
- `traces/investigate-m13-nobel-chemistry-2026.json` 是 M13 的完整轨迹（citations、claimMatrix、debate、trajectory、fusionNetwork）。
- 判定为 `UNVERIFIED` 是引擎的保守裁决（正反信号 0.8 / 0.0），不是“没查到”。

## 供应商真实连通

在工具与供应商页面点击“测试连接”：

| 供应商 | 结果 |
| --- | --- |
| 元宝 SearchPro（腾讯云 WSA） | `899 毫秒内有应答`；API 探针返回 `10 citations came back`（1118 ms） |
| DeepSeek | `451 毫秒内有应答`；API 探针返回 `2 entries visible to this key`（1138 ms） |

截图：`providerhub-search-tencent-real.png`（网页搜索通道货架，含真实结果）。

## FRAMES 公开题抽样实测

`benchmark/` 下是一次真实的小样本评测：FRAMES（google/frames-benchmark，test 集）前 20 题，
每题跑一次 M13 真实检索，再由 DeepSeek `deepseek-flash`（temperature 0）判分。

| 指标 | 真实值 |
| --- | --- |
| DeepSeek 判分准确率 | **15.0%**（3/20，上界） |
| 可信命中率（词边界、排除纯数字） | **0.0%** |
| 单题延迟 中位数 / p95 | 1904 ms / 5599 ms |
| 平均引用 / 权威引用 | 20 / 4.55 |
| 真实检索请求 | **770** 次（tencent 414、bing 240、youfree 84、arxiv 25、ddg 7） |
| 判定分布 | UNVERIFIED 17、VERIFIED 2、REFUTED 1 |

**必须说清楚的两点**（已逐题回查原始响应）：

1. 3 条判分 CORRECT 里有 2 条是假阳性（第 12、14 题）：引用页与标准答案毫无关系，
   只有第 8 题能在引用里找到词面对应。所以 15.0% 是上界，确定性口径是 **0%**。
2. 字符串命中会被子串误导（标准答案 `87` 命中 `1887`，`2` 命中 84 次偶然数字），
   因此另列了词边界口径。

结论：当前 M13 返回的是**证据包**而不是合成答案，FRAMES 这类多跳题大多卡在
“检索到了材料但没有把答案算出来”。这是下一步要补的能力，不粉饰。

## 文件

| 文件 | 说明 |
| --- | --- |
| `detective-m13-light-full.png` | M13 真实结果整页截图（浅色） |
| `providerhub-search-tencent-real.png` | 供应商平台 + 元宝真实连通测试 |
| `exe-icon-check.png` | 从 `molasearch.exe` 提取的图标（MolaMaker 角色） |
| `benchmark-landscape.html` / `.png` | AI 搜索能力基准图鉴（公开基准设定 + MolaSearch 实测） |
| `benchmark/` | FRAMES 抽样实测的题目、逐题轨迹、汇总与报告 |
| `pii-secret-audit.md` | 推送前的个人信息与密钥审计 |
| `traces/` | 可复跑的完整 JSON 轨迹 |

## 复跑

```powershell
# 1. 启动（网页 3430，网关 3432；agent 配置被隔离在仓库内的 agent-home/）
powershell -ExecutionPolicy Bypass -File scripts\start-web.ps1
# 2. 用 data\webkey.txt 里的 key 打开 http://127.0.0.1:3430/?k=<key>
# 3. 侦探台输入问题，选 M13，开始查证
```

> 说明：截图与轨迹中的引用是公开网页；应用输出门在写入前已替换其中的邮箱等个人信息（`输出隐私门 20/20`）。
