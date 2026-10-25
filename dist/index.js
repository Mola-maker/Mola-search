#!/usr/bin/env node
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { loadConfig } from "./config.js";
import { buildServer } from "./mcp/server.js";
import { log } from "./util/log.js";
async function main() {
    const config = loadConfig();
    const { server } = buildServer(config);
    await server.connect(new StdioServerTransport());
    log(`已启动 (dryRun=${config.dryRun}, endpoint=${config.endpoint}, qps=${config.qps}) — ` +
        (config.dryRun
            ? "干跑模式：不访问外网、不需要密钥"
            : "实连模式：使用 TENCENTCLOUD_SECRET_ID/KEY 调用 wsa SearchPro"));
}
main().catch((err) => {
    log(`启动失败: ${err?.stack ?? err}`);
    process.exit(1);
});
