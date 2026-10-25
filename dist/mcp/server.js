import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { loadConfig } from "../config.js";
import { WsaClient } from "../core/wsa-client.js";
import { SwarmEngine } from "../swarm/engine.js";
import { HistoryStore } from "../util/text.js";
import { loadRuntime, persistRuntime } from "./state.js";
import { makeSearchTool, makeSwarmTool, makeHistoryTool, makePluginManageTool } from "./tools.js";
import { registerDynamicResources, registerPrompts } from "./extras.js";
import { log } from "../util/log.js";
function createClient(config) {
    return new WsaClient({
        secretId: config.secretId,
        secretKey: config.secretKey,
        endpoint: config.endpoint,
        qps: config.qps,
        timeoutMs: config.timeoutMs,
        maxRetries: config.maxRetries,
        dryRun: config.dryRun,
    });
}
/**
 * 构建动态 MCP 服务器：
 *  - capabilities 声明 tools/resources/prompts 均支持 listChanged
 *  - reconfigure() 同步工具集合，reload() 同时重载配置和运行状态
 */
export function buildServer(config) {
    const server = new McpServer({ name: "yuanbao-swarm-search-mcp", version: "1.0.0" }, { capabilities: { tools: { listChanged: true }, resources: { listChanged: true }, prompts: { listChanged: true } } });
    const client = createClient(config);
    const history = new HistoryStore(config.dataDir);
    const engine = new SwarmEngine(client, history);
    const runtime = loadRuntime(config);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- 各工具 handler 入参类型不同，注册处统一走宽类型
    const TOOL_FACTORY = {
        yuanbao_search: () => makeSearchTool(ctx),
        swarm_iterative_search: () => makeSwarmTool(ctx),
        swarm_history: () => makeHistoryTool(ctx),
        plugin_manage: () => makePluginManageTool(ctx),
    };
    const TOOL_NAMES = Object.keys(TOOL_FACTORY);
    const registered = new Map();
    const ctx = {
        config,
        client,
        engine,
        history,
        enabledTools: runtime.enabledTools,
        customProfiles: runtime.customProfiles,
        persist: () => persistRuntime(ctx.config, ctx),
        reconfigure: () => reconfigure(),
        reload: () => reload(),
    };
    function reconfigure() {
        let changed = false;
        const sendToolListChanged = server.sendToolListChanged;
        server.sendToolListChanged = () => { };
        try {
            for (const [name, ref] of registered) {
                if (ctx.enabledTools.has(name))
                    continue;
                try {
                    ref.remove();
                }
                catch {
                    const mutableServer = server;
                    delete mutableServer._registeredTools?.[name];
                }
                registered.delete(name);
                changed = true;
            }
            for (const name of TOOL_NAMES) {
                if (!ctx.enabledTools.has(name) || registered.has(name))
                    continue;
                try {
                    const def = TOOL_FACTORY[name]();
                    const handle = server.registerTool(def.name, { title: def.title, description: def.description, inputSchema: def.inputSchema }, def.handler);
                    registered.set(name, handle);
                    changed = true;
                }
                catch (err) {
                    log(`注册工具 ${name} 失败: ${err.message}`);
                }
            }
        }
        finally {
            server.sendToolListChanged = sendToolListChanged;
        }
        if (changed) {
            try {
                sendToolListChanged.call(server);
            }
            catch {
                /* 无连接时忽略 */
            }
        }
        log(`工具集已同步: ${[...registered.keys()].join(", ") || "(无)"}`);
    }
    function reload() {
        const nextConfig = loadConfig();
        const nextRuntime = loadRuntime(nextConfig);
        const nextClient = createClient(nextConfig);
        const nextHistory = new HistoryStore(nextConfig.dataDir);
        const nextEngine = new SwarmEngine(nextClient, nextHistory);
        ctx.config = nextConfig;
        ctx.client = nextClient;
        ctx.history = nextHistory;
        ctx.engine = nextEngine;
        ctx.enabledTools = nextRuntime.enabledTools;
        ctx.customProfiles = nextRuntime.customProfiles;
        reconfigure();
    }
    reconfigure();
    registerDynamicResources(server, ctx);
    registerPrompts(server, ctx);
    // SIGHUP 热重载（Unix）；Windows 下可用 plugin_manage reload
    process.on("SIGHUP", () => {
        try {
            log("收到 SIGHUP，重载配置");
            reload();
        }
        catch (err) {
            log(`重载失败: ${err.message}`);
        }
    });
    return { server, ctx };
}
