#!/usr/bin/env node
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { HistoryStore } from "../dist/util/text.js";

const PROJECT_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const TEMP_ROOT = mkdtempSync(join(tmpdir(), "ysm-regression-"));
let passed = 0;
let total = 0;

async function test(name, fn) {
  total += 1;
  try {
    await fn();
    passed += 1;
    console.log(`PASS ${name}`);
  } catch (error) {
    console.error(`FAIL ${name}`);
    console.error(error?.stack ?? error);
  }
}

function historyRecord(id) {
  return {
    id,
    createdAt: new Date().toISOString(),
    query: "regression",
    profile: "quick-look",
    rounds: 1,
    totalCalls: 1,
    totalHits: 10,
    uniqueHits: 10,
    coverage: 1,
    report: { ok: true },
  };
}

function cleanEnvironment(dataDir) {
  const env = Object.fromEntries(
    Object.entries(process.env).filter(
      ([key]) => !/^YUANBAO_/i.test(key) && !/^TENCENT(?:CLOUD)?_SECRET_(?:ID|KEY)$/i.test(key),
    ),
  );
  env.YUANBAO_DRY_RUN = "1";
  env.YUANBAO_DATA_DIR = dataDir;
  return env;
}

function makeConfig(dataDir, profileName) {
  return {
    dryRun: true,
    endpoint: "http://127.0.0.1:9",
    dataDir,
    profiles: [
      {
        name: profileName,
        description: "regression fixture",
        strategy: "quick",
        maxRounds: 1,
        maxSubqueries: 1,
        searchersPerQuery: 1,
        coverageThreshold: 0,
        cnt: 10,
      },
    ],
  };
}

class McpClient {
  constructor(cwd, dataDir) {
    this.nextId = 1;
    this.pending = new Map();
    this.notifications = [];
    this.buffer = "";
    this.stderr = "";
    this.proc = spawn(process.execPath, [join(PROJECT_ROOT, "dist", "index.js")], {
      cwd,
      env: cleanEnvironment(dataDir),
      stdio: ["pipe", "pipe", "pipe"],
    });
    this.exit = new Promise((resolveExit) => {
      this.proc.once("exit", (code, signal) => {
        for (const { reject } of this.pending.values()) {
          reject(new Error(`MCP exited before response (code=${code}, signal=${signal})\n${this.stderr}`));
        }
        this.pending.clear();
        resolveExit();
      });
    });
    this.proc.stderr.on("data", (chunk) => {
      this.stderr += chunk.toString();
    });
    this.proc.stdout.on("data", (chunk) => this.onStdout(chunk));
  }

  onStdout(chunk) {
    this.buffer += chunk.toString();
    let newline;
    while ((newline = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.slice(0, newline).trim();
      this.buffer = this.buffer.slice(newline + 1);
      if (!line) continue;
      const message = JSON.parse(line);
      if (message.id !== undefined && this.pending.has(message.id)) {
        const { resolve: resolveRequest, timer } = this.pending.get(message.id);
        clearTimeout(timer);
        this.pending.delete(message.id);
        resolveRequest(message);
      } else if (message.method) {
        this.notifications.push(message);
      }
    }
  }

  request(method, params = {}, timeoutMs = 20000) {
    const id = this.nextId++;
    return new Promise((resolveRequest, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`Timeout waiting for ${method}\n${this.stderr}`));
      }, timeoutMs);
      this.pending.set(id, { resolve: resolveRequest, reject, timer });
      this.proc.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    });
  }

  async initialize() {
    const response = await this.request("initialize", {
      protocolVersion: "2025-06-18",
      capabilities: {},
      clientInfo: { name: "regression", version: "1.0.0" },
    });
    assert.equal(response.error, undefined, JSON.stringify(response.error));
    this.proc.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" })}\n`);
  }

  async callTool(name, args = {}) {
    const response = await this.request("tools/call", { name, arguments: args });
    assert.equal(response.error, undefined, JSON.stringify(response.error));
    assert.ok(response.result, `Missing tools/call result for ${name}`);
    return response.result;
  }

  async toolNames() {
    const response = await this.request("tools/list");
    assert.equal(response.error, undefined, JSON.stringify(response.error));
    return response.result.tools.map((tool) => tool.name);
  }

  listChangedCount() {
    return this.notifications.filter((message) => message.method === "notifications/tools/list_changed").length;
  }

  async close() {
    if (this.proc.exitCode === null && this.proc.signalCode === null) this.proc.kill();
    await Promise.race([this.exit, delay(1000)]);
  }
}

function makeFixture(profileName = "file-profile-v1") {
  const cwd = mkdtempSync(join(TEMP_ROOT, "mcp-"));
  const dataDir = join(cwd, "data");
  const configPath = join(cwd, ".yuanbao-swarm.json");
  writeFileSync(configPath, JSON.stringify(makeConfig(dataDir, profileName)), "utf8");
  return { cwd, dataDir, configPath, clients: [] };
}

async function startClient(fixture) {
  const client = new McpClient(fixture.cwd, fixture.dataDir);
  fixture.clients.push(client);
  await client.initialize();
  return client;
}

async function cleanupFixture(fixture) {
  await Promise.all(fixture.clients.map((client) => client.close()));
  rmSync(fixture.cwd, { recursive: true, force: true });
}

function reportFromResult(result) {
  assert.notEqual(result.isError, true, result.content?.[0]?.text);
  const text = result.content?.[0]?.text ?? "";
  const json = text.match(/```json\n([\s\S]*?)\n```/)?.[1];
  assert.ok(json, "Structured JSON report is missing");
  return JSON.parse(json);
}

await test("HistoryStore rejects path traversal IDs without reading or writing outside its directory", async () => {
  const dir = mkdtempSync(join(TEMP_ROOT, "history-traversal-"));
  const store = new HistoryStore(dir);
  const escaped = resolve(dir, "..", "..", "escaped.json");
  for (const id of ["../../escaped", "..\\..\\escaped", "swarm-../../escaped", "/tmp/escaped"]) {
    assert.throws(() => store.save(historyRecord(id)), /无效的历史 ID/);
    assert.equal(store.get(id), null);
  }
  assert.equal(readdirSync(dir).length, 0);
  assert.equal(await import("node:fs").then(({ existsSync }) => existsSync(escaped)), false);
});

await test("HistoryStore pruning preserves runtime.json", async () => {
  const dir = mkdtempSync(join(TEMP_ROOT, "history-runtime-"));
  const runtimePath = join(dir, "runtime.json");
  const runtime = JSON.stringify({ enabledTools: ["plugin_manage"], sentinel: "keep" });
  writeFileSync(runtimePath, runtime, "utf8");
  const store = new HistoryStore(dir);
  for (let i = 0; i < 51; i += 1) store.save(historyRecord(`swarm-${i.toString(36)}-1`));
  assert.equal(readFileSync(runtimePath, "utf8"), runtime);
  assert.equal(store.list(100).length, 50);
  assert.deepEqual(readdirSync(dir).filter((name) => name === "runtime.json"), ["runtime.json"]);
});

await test("HistoryStore save failure is surfaced with its cause", async () => {
  const dir = mkdtempSync(join(TEMP_ROOT, "history-save-failure-"));
  const blocker = join(dir, "not-a-directory");
  writeFileSync(blocker, "unchanged", "utf8");
  const store = new HistoryStore(blocker);
  assert.throws(
    () => store.save(historyRecord("swarm-save-1")),
    (error) => error.message === "保存历史记录失败: swarm-save-1" && error.cause instanceof Error,
  );
  assert.equal(readFileSync(blocker, "utf8"), "unchanged");
});

await test("MCP dry-run loads and executes a config-file Profile", async () => {
  const fixture = makeFixture();
  try {
    const client = await startClient(fixture);
    const listed = await client.callTool("plugin_manage", { action: "list_profiles" });
    const profiles = JSON.parse(listed.content[0].text);
    assert.ok(profiles.custom.some((profile) => profile.name === "file-profile-v1" && profile.cnt === 10));
    const result = await client.callTool("swarm_iterative_search", {
      query: "配置文件 Profile 回归",
      profile: "file-profile-v1",
    });
    assert.equal(reportFromResult(result).profile, "file-profile-v1");
  } finally {
    await cleanupFixture(fixture);
  }
});

await test("MCP dry-run rejects an unknown Profile", async () => {
  const fixture = makeFixture();
  try {
    const client = await startClient(fixture);
    const result = await client.callTool("swarm_iterative_search", {
      query: "unknown profile",
      profile: "does-not-exist",
    });
    assert.equal(result.isError, true);
    assert.match(result.content[0].text, /未知 Profile: does-not-exist/);
  } finally {
    await cleanupFixture(fixture);
  }
});

await test("MCP disabled tool remains disabled across restart", async () => {
  const fixture = makeFixture();
  try {
    const first = await startClient(fixture);
    const disabled = await first.callTool("plugin_manage", { action: "disable_tool", name: "yuanbao_search" });
    assert.notEqual(disabled.isError, true, disabled.content[0].text);
    assert.ok(!(await first.toolNames()).includes("yuanbao_search"));
    await first.close();

    const second = await startClient(fixture);
    assert.ok(!(await second.toolNames()).includes("yuanbao_search"));
    const status = await second.callTool("plugin_manage", { action: "status" });
    assert.ok(!JSON.parse(status.content[0].text).enabledTools.includes("yuanbao_search"));
  } finally {
    await cleanupFixture(fixture);
  }
});

await test("MCP emits exactly one list_changed notification per actual tool change", async () => {
  const fixture = makeFixture();
  try {
    const client = await startClient(fixture);
    assert.equal(client.listChangedCount(), 0);
    await client.callTool("plugin_manage", { action: "disable_tool", name: "yuanbao_search" });
    await delay(75);
    assert.equal(client.listChangedCount(), 1);
    await client.callTool("plugin_manage", { action: "disable_tool", name: "yuanbao_search" });
    await delay(75);
    assert.equal(client.listChangedCount(), 1);
    await client.callTool("plugin_manage", { action: "enable_tool", name: "yuanbao_search" });
    await delay(75);
    assert.equal(client.listChangedCount(), 2);
    await client.callTool("plugin_manage", { action: "enable_tool", name: "yuanbao_search" });
    await delay(75);
    assert.equal(client.listChangedCount(), 2);
  } finally {
    await cleanupFixture(fixture);
  }
});

await test("MCP reload reads changed config and uses the new Profile", async () => {
  const fixture = makeFixture("file-profile-v1");
  try {
    const client = await startClient(fixture);
    writeFileSync(fixture.configPath, JSON.stringify(makeConfig(fixture.dataDir, "file-profile-v2")), "utf8");
    const reloaded = await client.callTool("plugin_manage", { action: "reload" });
    assert.notEqual(reloaded.isError, true, reloaded.content[0].text);
    const listed = await client.callTool("plugin_manage", { action: "list_profiles" });
    const custom = JSON.parse(listed.content[0].text).custom.map((profile) => profile.name);
    assert.ok(custom.includes("file-profile-v2"));
    assert.ok(!custom.includes("file-profile-v1"));
    const result = await client.callTool("swarm_iterative_search", {
      query: "reload regression",
      profile: "file-profile-v2",
    });
    assert.equal(reportFromResult(result).profile, "file-profile-v2");
    assert.equal(client.listChangedCount(), 0);
  } finally {
    await cleanupFixture(fixture);
  }
});

await test("MCP report rawHits includes all pre-dedup dry-run hits", async () => {
  const fixture = makeFixture();
  try {
    const client = await startClient(fixture);
    const result = await client.callTool("swarm_iterative_search", {
      query: "rawHits regression",
      profile: "file-profile-v1",
    });
    const report = reportFromResult(result);
    assert.equal(report.stats.totalApiCalls, 1);
    assert.equal(report.stats.rawHits, 10);
    assert.equal(report.stats.rawHits, report.iterationTrace.reduce((sum, round) => sum + round.hits, 0));
    assert.ok(report.stats.rawHits >= report.stats.uniqueHits);
  } finally {
    await cleanupFixture(fixture);
  }
});

rmSync(TEMP_ROOT, { recursive: true, force: true });
console.log(`\nRegression: ${passed}/${total} passed`);
if (passed !== total) process.exitCode = 1;
