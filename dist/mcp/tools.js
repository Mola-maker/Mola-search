import { z } from "zod";
import { configuredCustomProfiles, normalizeProfile } from "./state.js";
import { BUILTIN_PROFILES, ALL_TOOL_NAMES, FRESHNESS_PATTERN } from "../config.js";
import { WsaError } from "../core/types.js";
import { log } from "../util/log.js";
function ok(text) {
    return { content: [{ type: "text", text }] };
}
function fail(err) {
    const msg = err instanceof WsaError
        ? `元宝搜索错误 [${err.code}]${err.requestId ? ` (RequestId: ${err.requestId})` : ""}: ${err.message}`
        : `插件错误: ${err?.message ?? String(err)}`;
    log(msg);
    return { content: [{ type: "text", text: msg }], isError: true };
}
/** ---------- 工具入参 Schema（对齐 SearchPro 文档约束） ---------- */
const freshnessSchema = z
    .string()
    .regex(FRESHNESS_PATTERN, "时效格式：d/m/y / d[1-30] / m[1-12] / y[1-5] / yyyy-mm-dd / yyyy-mm-dd,yyyy-mm-dd");
const cntSchema = z.number().int().refine((v) => [10, 20, 30, 40, 50].includes(v), "Cnt 仅支持 10/20/30/40/50");
const modeSchema = z.number().int().refine((v) => [0, 1, 2].includes(v), "Mode：0 公网 / 1 优质权威垂源(旗舰) / 2 混合(旗舰)");
const siteSchema = z
    .string()
    .refine((v) => !v.startsWith("exclude:") || v.slice(8).split("|").filter(Boolean).length <= 5, "exclude: 最多排除 5 个域名");
/** ---------- 工具 1：单次检索 ---------- */
export function makeSearchTool(ctx) {
    return {
        name: "yuanbao_search",
        title: "元宝搜索 · 单次检索",
        description: "调用腾讯元宝搜索（wsa SearchPro）做一次全网检索，返回 AI 友好的结构化结果" +
            "（标题/URL/动态摘要/相关性得分/权威等级/图片/延伸链接）。适合单点问题；需要多角度深挖请用 swarm_iterative_search。",
        inputSchema: {
            query: z.string().min(1).max(100).describe("搜索词（≤100 字符）"),
            mode: modeSchema.optional().describe("搜索模式：0 公网全量（默认）、1 优质权威垂源（旗舰版）、2 混合（旗舰版）"),
            site: siteSchema.optional().describe('指定站点（如 "qq.com"）或排除站点（如 "exclude:qq.com|sohu.com"，最多排除 5 个）'),
            cnt: cntSchema.optional().describe("返回条数：10/20/30/40/50（默认 10）"),
            industry: z.enum(["gov", "news", "acad", "finance"]).optional().describe("行业定向：政府/新闻/学术/财经"),
            freshness: freshnessSchema.optional().describe("时效过滤：d/m/y、d[1-30]天 / m[1-12]月 / y[1-5]年 / 日期或日期区间"),
            deeplinks: z.boolean().optional().describe("是否返回主站延伸子链接（仅旗舰版生效）"),
        },
        handler: async (args) => {
            try {
                const { response, stats } = await ctx.client.search({
                    query: args.query,
                    mode: args.mode,
                    site: args.site,
                    cnt: args.cnt,
                    industry: args.industry,
                    freshness: args.freshness,
                    deeplinks: args.deeplinks,
                });
                const lines = [
                    `# 元宝搜索结果（${response.pages.length} 条 | 版本 ${response.version} | ${stats.cacheHit ? "缓存命中" : `${stats.latencyMs}ms`})`,
                    response.msg ? `> 提示: ${response.msg}` : "",
                    ...response.pages.map((p, i) => [
                        `## ${i + 1}. ${p.title ?? "(无标题)"}`,
                        `- URL: ${p.url ?? ""}`,
                        `- 站点: ${p.site ?? ""} | 日期: ${p.date ?? ""} | 相关性: ${p.score ?? "?"}${p.authority_level !== undefined ? ` | 权威等级: ${p.authority_level}/5` : ""}`,
                        p.content || p.passage ? `- 摘要: ${((p.content || p.passage) ?? "").slice(0, 400)}` : "",
                        p.pics?.length ? `- 图片: ${p.pics.map((x) => x.origin_url).filter(Boolean).join(", ")}` : "",
                        p.deeplinks?.length ? `- 延伸: ${p.deeplinks.map((d) => `${d.title}(${d.url})`).join("; ")}` : "",
                    ]
                        .filter(Boolean)
                        .join("\n")),
                ];
                return ok(lines.filter(Boolean).join("\n"));
            }
            catch (err) {
                return fail(err);
            }
        },
    };
}
/** ---------- 工具 2：群智能体迭代深搜 ---------- */
export function makeSwarmTool(ctx) {
    return {
        name: "swarm_iterative_search",
        title: "群智能体 · 迭代深搜",
        description: "群智能体迭代搜索：Planner 将问题分解为多个方面，Searcher Swarm 以不同策略（公网/混合/行业/时效）并行多路检索，" +
            "Analyst 逐轮计算覆盖率并定位盲区，自动派生精化子查询迭代补盲，直到收敛或达轮次上限，最后由 Synthesizer 输出" +
            "含迭代轨迹、方面聚类发现、关键事实、引用与遗留盲区的完整报告。适合复杂/多面/需要穷尽性的研究型问题。",
        inputSchema: {
            query: z.string().min(1).describe("研究问题（可用中文逗号/顿号/vs 分隔多个子主题，Planner 会自动分解）"),
            profile: z.string().optional().describe("Swarm Profile 名称，默认 deep-research；可用 plugin_manage list_profiles 查看"),
            max_rounds: z.number().int().min(1).max(5).optional().describe("覆盖 Profile 的最大迭代轮次（1~5）"),
            max_subqueries: z.number().int().min(1).max(8).optional().describe("覆盖 Profile 的每轮子查询上限（1~8）"),
            coverage_threshold: z.number().min(0).max(1).optional().describe("覆盖阈值（0~1），达到即提前收敛"),
            cnt: cntSchema.optional(),
            mode: modeSchema.optional(),
            site: siteSchema.optional(),
            industry: z.enum(["gov", "news", "acad", "finance"]).optional(),
            freshness: freshnessSchema.optional(),
        },
        handler: async (args, 
        // eslint-disable-next-line @typescript-eslint/no-explicit-any -- RequestHandlerExtra 版本间字段有差异，取宽类型
        extra) => {
            try {
                const profile = resolveProfile(ctx, args.profile);
                const effective = {
                    ...profile,
                    maxRounds: args.max_rounds ?? profile.maxRounds,
                    maxSubqueries: args.max_subqueries ?? profile.maxSubqueries,
                    coverageThreshold: args.coverage_threshold ?? profile.coverageThreshold,
                    cnt: (args.cnt ?? profile.cnt),
                };
                const progressToken = extra?._meta?.progressToken;
                const onProgress = async (p) => {
                    if (progressToken === undefined || !extra?.sendNotification)
                        return;
                    try {
                        await extra.sendNotification({
                            method: "notifications/progress",
                            params: { progressToken, progress: p.ratio, total: 1, message: p.message },
                        });
                    }
                    catch {
                        /* 进度通知失败不影响主流程 */
                    }
                };
                const { report, historyId } = await ctx.engine.run({
                    query: args.query,
                    profile: effective,
                    overrides: {
                        mode: args.mode,
                        site: args.site,
                        cnt: args.cnt,
                        industry: args.industry,
                        freshness: args.freshness,
                    },
                    onProgress,
                    signal: extra?.signal,
                });
                const summary = [
                    `# 群智能体迭代搜索报告 · ${report.query}`,
                    `> Profile: ${report.profile} | 轮次: ${report.roundsExecuted} | 收敛: ${report.converged}（${report.convergenceReason}）`,
                    `> API 调用 ${report.stats.totalApiCalls} 次（缓存命中 ${report.stats.cacheHits}）| 原始命中 ${report.stats.rawHits} → 去重后 ${report.stats.uniqueHits} | 总覆盖率 ${(report.overallCoverage * 100).toFixed(1)}% | 耗时 ${report.stats.elapsedMs}ms | 历史 ID: ${historyId}`,
                    "",
                    "## 方面覆盖",
                    ...report.coverage.map((c) => `- ${c.label}: ${(c.coverage * 100).toFixed(0)}%`),
                    "",
                    "## 关键发现",
                    ...report.findings.flatMap((f) => [
                        `### ${f.aspectLabel}`,
                        ...f.topPages.map((p, i) => `${i + 1}. [${p.title}](${p.url}) — ${p.site} ${p.date} ${p.authority != null ? `权威${p.authority}/5` : ""}\n   ${p.snippet}`),
                    ]),
                    "",
                    "## 关键事实摘录",
                    ...report.keyFacts.map((s, i) => `${i + 1}. ${s}`),
                    "",
                    "## 遗留盲区",
                    ...(report.openGaps.length ? report.openGaps : ["（无，全部方面达到覆盖阈值）"]),
                ].join("\n");
                return ok(summary + "\n\n<details><summary>完整结构化报告(JSON)</summary>\n\n```json\n" + JSON.stringify(report, null, 2) + "\n```\n</details>");
            }
            catch (err) {
                return fail(err);
            }
        },
    };
}
function resolveProfile(ctx, name) {
    const wanted = name ?? "deep-research";
    const custom = ctx.customProfiles.get(wanted);
    if (custom)
        return custom;
    const configured = ctx.config.profiles.find((profile) => profile.name === wanted);
    if (configured)
        return configured;
    throw new Error(`未知 Profile: ${wanted}，可用 plugin_manage list_profiles 查看可用名称`);
}
/** ---------- 工具 3：历史查询 ---------- */
export function makeHistoryTool(ctx) {
    return {
        name: "swarm_history",
        title: "深搜历史",
        description: "查看群智能体迭代搜索的历史记录（列表或按 ID 取完整报告）。",
        inputSchema: {
            action: z.enum(["list", "get"]).default("list").describe("list 列出最近记录；get 按 id 取完整报告"),
            id: z.string().optional().describe("action=get 时的记录 ID"),
            limit: z.number().int().min(1).max(50).optional().describe("list 的条数上限，默认 20"),
        },
        handler: async (args) => {
            try {
                if (args.action === "get") {
                    if (!args.id)
                        return fail(new Error("action=get 需要提供 id"));
                    const rec = ctx.history.get(args.id);
                    if (!rec)
                        return fail(new Error(`未找到记录 ${args.id}`));
                    return ok(JSON.stringify(rec, null, 2));
                }
                const items = ctx.history.list(args.limit ?? 20);
                return ok(JSON.stringify({ count: items.length, items }, null, 2));
            }
            catch (err) {
                return fail(err);
            }
        },
    };
}
/** ---------- 工具 4：插件动态管理（动态 MCP 核心） ---------- */
export function makePluginManageTool(ctx) {
    return {
        name: "plugin_manage",
        title: "插件动态管理",
        description: "动态管理本 MCP 插件：启停工具、注册/删除 Swarm Profile、重载配置、查看状态。工具集合变化时通过 tools/list_changed 通知客户端。",
        inputSchema: {
            action: z
                .enum(["status", "list_tools", "enable_tool", "disable_tool", "list_profiles", "register_profile", "remove_profile", "reload"])
                .describe("管理动作"),
            name: z.string().optional().describe("工具名（enable/disable_tool）或 Profile 名（remove_profile）"),
            profile: z.record(z.string(), z.unknown()).optional().describe("register_profile 时的 Profile 定义（name 必填，见 README 字段说明）"),
        },
        handler: async (args) => {
            try {
                switch (args.action) {
                    case "status":
                        return ok(JSON.stringify({
                            version: "1.0.0",
                            dryRun: ctx.config.dryRun,
                            endpoint: ctx.config.endpoint,
                            qps: ctx.config.qps,
                            enabledTools: [...ctx.enabledTools],
                            builtinProfiles: BUILTIN_PROFILES.map((profile) => profile.name),
                            customProfiles: configuredCustomProfiles(ctx).map((profile) => profile.name),
                        }, null, 2));
                    case "list_tools":
                        return ok(JSON.stringify({ all: ALL_TOOL_NAMES, enabled: [...ctx.enabledTools] }, null, 2));
                    case "enable_tool":
                    case "disable_tool": {
                        const name = args.name ?? "";
                        if (!ALL_TOOL_NAMES.includes(name)) {
                            return fail(new Error(`未知工具 ${name}，可选：${ALL_TOOL_NAMES.join(", ")}`));
                        }
                        const changed = args.action === "enable_tool" ? !ctx.enabledTools.has(name) : ctx.enabledTools.has(name);
                        if (changed) {
                            if (args.action === "enable_tool")
                                ctx.enabledTools.add(name);
                            else
                                ctx.enabledTools.delete(name);
                            ctx.persist();
                            ctx.reconfigure();
                        }
                        return ok(JSON.stringify({ ok: true, action: args.action, tool: name, enabled: [...ctx.enabledTools] }, null, 2));
                    }
                    case "list_profiles":
                        return ok(JSON.stringify({ builtin: BUILTIN_PROFILES, custom: configuredCustomProfiles(ctx) }, null, 2));
                    case "register_profile": {
                        const raw = args.profile ?? {};
                        const profile = normalizeProfile({ ...raw, name: raw.name ?? args.name });
                        if (!profile.name || profile.name === "unnamed")
                            return fail(new Error("profile.name 必填"));
                        ctx.customProfiles.set(profile.name, profile);
                        ctx.persist();
                        return ok(JSON.stringify({ ok: true, registered: profile }, null, 2));
                    }
                    case "remove_profile": {
                        const name = args.name ?? "";
                        if (!ctx.customProfiles.has(name))
                            return fail(new Error(`运行时 Profile 不存在: ${name}（内置及配置文件 Profile 不可删除）`));
                        ctx.customProfiles.delete(name);
                        ctx.persist();
                        return ok(JSON.stringify({ ok: true, removed: name }));
                    }
                    case "reload": {
                        ctx.reload();
                        return ok(JSON.stringify({ ok: true, reloaded: true, enabled: [...ctx.enabledTools] }, null, 2));
                    }
                    default:
                        return fail(new Error(`未知 action: ${args.action}`));
                }
            }
            catch (err) {
                return fail(err);
            }
        },
    };
}
