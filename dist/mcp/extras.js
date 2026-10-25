import z from "zod";
import { ResourceTemplate } from "@modelcontextprotocol/sdk/server/mcp.js";
import { configuredCustomProfiles } from "./state.js";
import { log } from "../util/log.js";
/** 动态资源注册（读取 RunContext 中的最新状态） */
export function registerDynamicResources(server, ctx) {
    // 服务器信息（静态）
    server.registerResource("server-info", "yuanbao://server/info", { title: "插件服务器信息", description: "元宝搜索群智能体 MCP 插件的运行状态与配置", mimeType: "application/json" }, async () => ({
        contents: [
            {
                uri: "yuanbao://server/info",
                mimeType: "application/json",
                text: JSON.stringify({
                    name: "yuanbao-swarm-search-mcp",
                    version: "1.0.0",
                    api: { endpoint: ctx.config.endpoint, action: "SearchPro", version: "2025-05-08", service: "wsa" },
                    dryRun: ctx.config.dryRun,
                    qps: ctx.config.qps,
                    enabledTools: [...ctx.enabledTools],
                    profiles: { custom: configuredCustomProfiles(ctx).map((profile) => profile.name) },
                }, null, 2),
            },
        ],
    }));
    // 历史记录模板资源：yuanbao://history/{id}，列表随历史增长动态变化
    server.registerResource("swarm-history", new ResourceTemplate("yuanbao://history/{id}", {
        list: async () => ({
            resources: ctx.history.list(50).map((h) => ({
                uri: `yuanbao://history/${h.id}`,
                name: `深搜记录 ${h.id}`,
                description: `${h.query}（profile=${h.profile}, rounds=${h.rounds}, uniqueHits=${h.uniqueHits}）`,
                mimeType: "application/json",
            })),
        }),
    }), { title: "群智能体深搜历史", description: "按 ID 读取历史迭代搜索的完整报告", mimeType: "application/json" }, async (uri, { id }) => {
        const rec = id ? ctx.history.get(String(id)) : null;
        if (!rec) {
            return {
                contents: [{ uri: uri.toString(), mimeType: "application/json", text: JSON.stringify({ error: "not found", id }) }],
            };
        }
        return {
            contents: [{ uri: uri.toString(), mimeType: "application/json", text: JSON.stringify(rec, null, 2) }],
        };
    });
    log("resources 注册完成: yuanbao://server/info, yuanbao://history/{id}");
}
/** Prompt 模板：把迭代深搜的最佳实践注入到会话 */
export function registerPrompts(server, _ctx) {
    server.registerPrompt("deep-research-brief", {
        title: "深度研究简报",
        description: "针对一个主题生成群智能体迭代深搜简报（自动分解→并行检索→补盲→汇总）",
        argsSchema: {
            topic: z.string().min(1).describe("研究主题"),
            depth: z.enum(["quick", "standard", "exhaustive"]).optional().describe("深度档位"),
        },
    }, ({ topic, depth }) => ({
        messages: [
            {
                role: "user",
                content: {
                    type: "text",
                    text: `请使用 swarm_iterative_search 工具对主题「${topic}」做群智能体迭代深搜，` +
                        `深度档位=${depth ?? "standard"}（quick→profile quick-look；standard→deep-research；exhaustive→max_rounds=5 且 coverage_threshold=0.85）。` +
                        "拿到报告后：1) 按 findings 分方面归纳要点并附引用编号；2) 列出 keyFacts；3) 若存在 openGaps，用 yuanbao_search 对盲区补 1~2 次检索后再汇总；" +
                        "4) 最后给出结论与信息可信度评估（结合权威等级 authority_level）。",
                },
            },
        ],
    }));
    server.registerPrompt("gap-followup", {
        title: "盲区补搜",
        description: "读取一次历史深搜报告的遗留盲区并生成补充检索计划",
        argsSchema: { history_id: z.string().min(1).describe("swarm_history 记录 ID") },
    }, ({ history_id }) => ({
        messages: [
            {
                role: "user",
                content: {
                    type: "text",
                    text: `请先用 swarm_history(action=get, id="${history_id}") 取回历史报告，` +
                        "然后针对其中 openGaps 列出的每个盲区，用 yuanbao_search 或 swarm_iterative_search 补充检索，" +
                        "并将新信息与原报告 findings 合并，输出更新后的结论。",
                },
            },
        ],
    }));
    log("prompts 注册完成: deep-research-brief, gap-followup");
}
