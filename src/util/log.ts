/** 所有日志走 stderr —— stdout 保留给 MCP 协议 */
export function log(...args: unknown[]): void {
  process.stderr.write(`[yuanbao-swarm-mcp] ${args.map(String).join(" ")}\n`);
}
