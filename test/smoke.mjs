#!/usr/bin/env node
/**
 * 冒烟测试：以 stdio 客户端身份与 dist/index.js 全链路对话（干跑模式，无需密钥/外网）。
 * 覆盖：initialize → tools/list → yuanbao_search → swarm_iterative_search（多轮迭代）
 *      → swarm_history → plugin_manage 动态启停工具 → resources/list → prompts/list
 */
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

const ROOT = new URL("..", import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1");
const DATA_DIR = mkdtempSync(join(tmpdir(), "ysm-smoke-"));

const proc = spawn(process.execPath, [join(ROOT, "dist", "index.js")], {
  env: { ...process.env, YUANBAO_DRY_RUN: "1", YUANBAO_DATA_DIR: DATA_DIR },
  stdio: ["pipe", "pipe", "pipe"],
});
let stderr = "";
proc.stderr.on("data", (d) => (stderr += d.toString()));

let nextId = 1;
const pending = new Map();
const notifications = [];
let buffer = "";

proc.stdout.on("data", (chunk) => {
  buffer += chunk.toString();
  let idx;
  while ((idx = buffer.indexOf("\n")) >= 0) {
    const line = buffer.slice(0, idx).trim();
    buffer = buffer.slice(idx + 1);
    if (!line) continue;
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      continue;
    }
    if (msg.id !== undefined && pending.has(msg.id)) {
      const { resolve } = pending.get(msg.id);
      pending.delete(msg.id);
      resolve(msg);
    } else if (msg.method) {
      notifications.push(msg);
    }
  }
});

function request(method, params, timeoutMs = 60000) {
  const id = nextId++;
  const payload = JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n";
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`timeout waiting ${method}`));
    }, timeoutMs);
    pending.set(id, {
      resolve: (msg) => {
        clearTimeout(timer);
        resolve(msg);
      },
    });
    proc.stdin.write(payload);
  });
}

const results = [];
function check(name, cond, detail = "") {
  results.push({ name, pass: Boolean(cond), detail });
  console.log(`${cond ? "✅" : "❌"} ${name}${detail ? ` — ${detail}` : ""}`);
}

try {
  // 1. initialize
  const init = await request("initialize", {
    protocolVersion: "2025-06-18",
    capabilities: {},
    clientInfo: { name: "smoke", version: "0.0.1" },
  });
  check("initialize 握手", init.result?.serverInfo?.name === "yuanbao-swarm-search-mcp", `version=${init.result?.serverInfo?.version}`);
  proc.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n");

  // 2. tools/list
  const list1 = await request("tools/list", {});
  const names1 = (list1.result?.tools ?? []).map((t) => t.name).sort();
  check(
    "tools/list 返回 4 个工具",
    JSON.stringify(names1) === JSON.stringify(["plugin_manage", "swarm_history", "swarm_iterative_search", "yuanbao_search"].sort()),
    names1.join(","),
  );

  // 3. 单次检索（干跑夹具）
  const s1 = await request("tools/call", {
    name: "yuanbao_search",
    arguments: { query: "量子计算", cnt: 10, mode: 2, freshness: "d30" },
  });
  const s1text = s1.result?.content?.[0]?.text ?? "";
  check("yuanbao_search 返回结果列表", s1.result && !s1.result.isError && s1text.includes("量子计算"), `chars=${s1text.length}`);

  // 3b. 参数校验：非法 cnt 应报错
  const s1b = await request("tools/call", { name: "yuanbao_search", arguments: { query: "x", cnt: 17 } });
  check("yuanbao_search 非法 Cnt 被拒绝", s1b.result?.isError === true, (s1b.result?.content?.[0]?.text ?? "").slice(0, 60));

  // 4. 群智能体迭代深搜（复合查询触发多方面分解 + 迭代）
  const s2 = await request(
    "tools/call",
    {
      name: "swarm_iterative_search",
      arguments: { query: "大模型 vs 智能体框架", profile: "deep-research", max_rounds: 3 },
    },
    120000,
  );
  const s2text = s2.result?.content?.[0]?.text ?? "";
  const report = s2text.match(/```json\n([\s\S]*?)\n```/)?.[1];
  let rep = null;
  try {
    rep = JSON.parse(report ?? "null");
  } catch {
    /* ignore */
  }
  check("swarm_iterative_search 产出报告", Boolean(rep?.iterationTrace), `rounds=${rep?.roundsExecuted}, uniqueHits=${rep?.stats?.uniqueHits}`);
  check("报告含迭代轨迹与覆盖率", Array.isArray(rep?.coverage) && rep?.coverage?.length > 0 && typeof rep?.overallCoverage === "number", `coverage=${rep?.overallCoverage}`);
  check("报告含发现聚类", (rep?.findings ?? []).length > 0, `findings=${rep?.findings?.length}`);

  // 5. 历史
  const h1 = await request("tools/call", { name: "swarm_history", arguments: { action: "list" } });
  const h1json = JSON.parse(h1.result?.content?.[0]?.text ?? "{}");
  check("swarm_history list 有记录", (h1json.items ?? []).length >= 1, `count=${h1json.count}`);

  // 6. 动态管理：禁用工具 → tools/list 变化 → 重新启用
  const d1 = await request("tools/call", { name: "plugin_manage", arguments: { action: "disable_tool", name: "yuanbao_search" } });
  check("disable_tool 成功", d1.result && !d1.result.isError);
  await delay(200);
  const list2 = await request("tools/list", {});
  const names2 = (list2.result?.tools ?? []).map((t) => t.name);
  check("禁用后 tools/list 不再含 yuanbao_search", !names2.includes("yuanbao_search"), names2.join(","));
  const gotNotification = notifications.some((n) => n.method === "notifications/tools/list_changed");
  check("广播 tools/list_changed 通知", gotNotification);

  const d2 = await request("tools/call", { name: "plugin_manage", arguments: { action: "enable_tool", name: "yuanbao_search" } });
  check("enable_tool 恢复", d2.result && !d2.result.isError);

  // 6b. 动态注册自定义 Profile
  const p1 = await request("tools/call", {
    name: "plugin_manage",
    arguments: {
      action: "register_profile",
      profile: {
        name: "my-profile",
        description: "冒烟测试自定义档",
        strategy: "news",
        maxRounds: 2,
        maxSubqueries: 3,
        searchersPerQuery: 2,
        coverageThreshold: 0.6,
        cnt: 10,
        industry: "news",
        freshness: "d7",
      },
    },
  });
  check("register_profile 自定义档注册", p1.result && !p1.result.isError);
  const s3 = await request("tools/call", {
    name: "swarm_iterative_search",
    arguments: { query: "新能源车市况", profile: "my-profile" },
  }, 120000);
  check("自定义 Profile 可用于深搜", s3.result && !s3.result.isError, (s3.result?.content?.[0]?.text ?? "").slice(0, 80).replace(/\n/g, " "));

  // 7. resources / prompts
  const r1 = await request("resources/list", {});
  const ruris = (r1.result?.resources ?? []).map((r) => r.uri);
  check("resources/list 含 server-info 与历史模板", ruris.some((u) => String(u).includes("yuanbao://server/info")), ruris.join(", "));
  const pr1 = await request("prompts/list", {});
  const prnames = (pr1.result?.prompts ?? []).map((p) => p.name);
  check("prompts/list 含研究简报与补搜模板", prnames.includes("deep-research-brief") && prnames.includes("gap-followup"), prnames.join(","));
} catch (err) {
  check("测试流程无异常", false, (err?.stack ?? String(err)).slice(0, 400));
} finally {
  proc.kill();
  await delay(200);
  try {
    rmSync(DATA_DIR, { recursive: true, force: true });
  } catch {
    /* ignore */
  }
}

const failed = results.filter((r) => !r.pass);
console.log(`\n==== 冒烟测试：${results.length - failed.length}/${results.length} 通过 ====`);
if (failed.length > 0) {
  console.log("\n---- server stderr 尾部 ----\n" + stderr.split("\n").slice(-15).join("\n"));
  process.exit(1);
}
