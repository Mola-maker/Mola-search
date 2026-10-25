#!/usr/bin/env node
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { request as httpRequest } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { once } from "node:events";
import { setTimeout as delay } from "node:timers/promises";

const ROOT = fileURLToPath(new URL("..", import.meta.url));
const DATA_DIR = await mkdtemp(join(tmpdir(), "ysm-web-"));
const results = [];
let proc;
let stderr = "";
let stdout = "";
let baseUrl;

async function availablePort() {
  const server = createServer();
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  assert(address && typeof address === "object");
  const { port } = address;
  server.close();
  await once(server, "close");
  return port;
}

async function stopChild() {
  if (!proc || proc.exitCode !== null || proc.signalCode !== null) return;
  const exited = once(proc, "exit");
  proc.kill("SIGTERM");
  const stopped = await Promise.race([exited.then(() => true), delay(2000).then(() => false)]);
  if (!stopped) {
    proc.kill("SIGKILL");
    await exited;
  }
}

async function waitUntilReady() {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (proc.exitCode !== null || proc.signalCode !== null) {
      throw new Error(`Web 服务提前退出\nstdout: ${stdout}\nstderr: ${stderr}`);
    }
    try {
      const response = await fetch(`${baseUrl}/api/health`, { signal: AbortSignal.timeout(500) });
      if (response.ok) return;
    } catch {
      // 服务尚未开始监听。
    }
    await delay(50);
  }
  throw new Error(`等待 Web 服务启动超时\nstdout: ${stdout}\nstderr: ${stderr}`);
}

async function api(path, options) {
  return fetch(`${baseUrl}${path}`, { ...options, signal: AbortSignal.timeout(30000) });
}

async function rawRequest(path) {
  const url = new URL(baseUrl);
  return new Promise((resolve, reject) => {
    const req = httpRequest(
      { hostname: url.hostname, port: url.port, path, method: "GET" },
      (res) => {
        const chunks = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) }));
      },
    );
    req.setTimeout(5000, () => req.destroy(new Error("原始 HTTP 请求超时")));
    req.on("error", reject);
    req.end();
  });
}

function parseNdjson(text) {
  assert(text.endsWith("\n"), "NDJSON 必须以换行结束");
  const lines = text.split("\n").slice(0, -1);
  assert(lines.length > 0, "NDJSON 不得为空");
  return lines.map((line) => JSON.parse(line));
}

function assertNoSniff(response) {
  assert.equal(response.headers.get("x-content-type-options"), "nosniff");
}

async function check(name, fn) {
  try {
    await fn();
    results.push({ name, pass: true });
    console.log(`✅ ${name}`);
  } catch (error) {
    results.push({ name, pass: false, error });
    console.error(`❌ ${name}\n${error?.stack ?? String(error)}`);
  }
}

try {
  const port = await availablePort();
  baseUrl = `http://127.0.0.1:${port}`;
  const env = {
    ...process.env,
    PORT: String(port),
    YUANBAO_DRY_RUN: "1",
    YUANBAO_DATA_DIR: DATA_DIR,
    YUANBAO_ENDPOINT: "http://127.0.0.1:1",
    TENCENTCLOUD_SECRET_ID: "",
    TENCENTCLOUD_SECRET_KEY: "",
    TENCENT_SECRET_ID: "",
    TENCENT_SECRET_KEY: "",
  };
  proc = spawn(process.execPath, [join(ROOT, "dist", "web", "server.js")], {
    cwd: DATA_DIR,
    env,
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  });
  proc.stdout.on("data", (chunk) => (stdout += chunk.toString()));
  proc.stderr.on("data", (chunk) => (stderr += chunk.toString()));
  await waitUntilReady();

  await check("health 返回干跑状态与 API 禁缓存头", async () => {
    const response = await api("/api/health");
    assert.equal(response.status, 200);
    assert.match(response.headers.get("content-type") ?? "", /^application\/json/);
    assert.equal(response.headers.get("cache-control"), "no-store");
    assert.equal(response.headers.get("pragma"), "no-cache");
    assert.equal(response.headers.get("expires"), "0");
    assert.deepEqual(await response.json(), { ok: true, dryRun: true });
  });

  await check("静态 index 内容、MIME 与安全响应头", async () => {
    const [response, expected] = await Promise.all([api("/"), readFile(join(ROOT, "dist-web", "index.html"))]);
    assert.equal(response.status, 200);
    assert.match(response.headers.get("content-type") ?? "", /^text\/html; charset=utf-8$/);
    assert.equal(response.headers.get("cache-control"), "no-cache");
    assertNoSniff(response);
    assert.deepEqual(Buffer.from(await response.arrayBuffer()), expected);
  });

  await check("官方 PNG 字节完整且 MIME 正确", async () => {
    const [response, expected] = await Promise.all([
      api("/assets/yuanbao-official.png"),
      readFile(join(ROOT, "dist-web", "assets", "yuanbao-official.png")),
    ]);
    assert.equal(response.status, 200);
    assert.equal(response.headers.get("content-type"), "image/png");
    assert.equal(response.headers.get("content-length"), String(expected.length));
    assertNoSniff(response);
    const body = Buffer.from(await response.arrayBuffer());
    assert.deepEqual(body.subarray(0, 8), Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]));
    assert.deepEqual(body, expected);
  });

  await check("single NDJSON 含完整阶段和最终结果", async () => {
    const response = await api("/api/search", {
      method: "POST",
      headers: { "Content-Type": "application/json; charset=utf-8" },
      body: JSON.stringify({ query: "量子计算", kind: "single", mode: 2, cnt: 10, freshness: "d30" }),
    });
    assert.equal(response.status, 200);
    assert.match(response.headers.get("content-type") ?? "", /^application\/x-ndjson; charset=utf-8$/);
    assert.equal(response.headers.get("cache-control"), "no-store");
    assertNoSniff(response);
    const events = parseNdjson(await response.text());
    assert.deepEqual(events.map((event) => event.type), ["stage", "result"]);
    assert.equal(events[0].stage, "searching");
    const result = events.at(-1).result;
    assert.equal(result.kind, "single");
    assert.equal(result.query, "量子计算");
    assert.ok(Array.isArray(result.pages) && result.pages.length > 0);
    assert.ok(result.pages.every((page) => page.title && page.url && page.passage));
    assert.match(result.requestId, /^dryrun-/);
    assert.equal(result.stats.attempts, 1);
    assert.equal(typeof result.stats.cacheHit, "boolean");
  });

  await check("swarm NDJSON 含规划、检索、分析、综合及完整报告", async () => {
    const response = await api("/api/search", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ query: "大模型与智能体框架", kind: "swarm", profile: "quick-look", cnt: 10 }),
    });
    assert.equal(response.status, 200);
    assertNoSniff(response);
    const events = parseNdjson(await response.text());
    assert.equal(events.at(-1).type, "result");
    assert.equal(events.filter((event) => event.type === "result").length, 1);
    const stages = events.filter((event) => event.type === "stage").map((event) => event.stage);
    assert.equal(stages[0], "planning");
    assert.equal(stages.at(-1), "synthesizing");
    for (const required of ["searching", "analyzing", "synthesizing"]) assert.ok(stages.includes(required), `缺少阶段 ${required}`);
    const result = events.at(-1).result;
    assert.equal(result.kind, "swarm");
    assert.equal(result.query, "大模型与智能体框架");
    assert.ok(Array.isArray(result.pages));
    assert.match(result.historyId, /^swarm-/);
    assert.equal(result.report.profile, "quick-look");
    assert.ok(Array.isArray(result.report.iterationTrace) && result.report.iterationTrace.length > 0);
    assert.ok(Array.isArray(result.report.findings) && result.report.findings.length > 0);
    assert.equal(typeof result.report.overallCoverage, "number");
    assert.ok(result.stats.totalApiCalls > 0);
  });

  await check("输入错误返回正确 HTTP 状态及 NDJSON 错误事件", async () => {
    const cases = [
      { body: "{", status: 400, code: "InvalidJson", type: "application/json" },
      { body: JSON.stringify({ query: " ", kind: "single" }), status: 400, code: "InvalidParameter", type: "application/json" },
      { body: JSON.stringify({ query: "x", kind: "other" }), status: 400, code: "InvalidParameter", type: "application/json" },
      { body: JSON.stringify({ query: "x", kind: "single", cnt: 17 }), status: 400, code: "InvalidParameter", type: "application/json" },
      { body: JSON.stringify({ query: "x", kind: "swarm", profile: "missing" }), status: 400, code: "InvalidParameter", type: "application/json" },
      { body: "{}", status: 415, code: "UnsupportedMediaType", type: "text/plain" },
    ];
    for (const item of cases) {
      const response = await api("/api/search", {
        method: "POST",
        headers: { "Content-Type": item.type },
        body: item.body,
      });
      assert.equal(response.status, item.status);
      assert.match(response.headers.get("content-type") ?? "", /^application\/x-ndjson/);
      const events = parseNdjson(await response.text());
      assert.equal(events.length, 1);
      assert.equal(events[0].type, "error");
      assert.equal(events[0].error.code, item.code);
      assert.equal(typeof events[0].error.message, "string");
    }
  });

  await check("静态路径穿越与非法编码被拒绝", async () => {
    for (const path of ["/..%2fpackage.json", "/%2e%2e%5cpackage.json", "/assets/%2e%2e/%2e%2e/package.json"]) {
      const response = await rawRequest(path);
      assert.ok([403, 404].includes(response.status), `${path} 返回 ${response.status}`);
      assert.ok(["Forbidden", "NotFound"].includes(JSON.parse(response.body.toString()).error.code));
      assert.notDeepEqual(response.body, await readFile(join(ROOT, "package.json")));
    }
    const invalid = await rawRequest("/%E0%A4%A");
    assert.equal(invalid.status, 400);
    assert.equal(JSON.parse(invalid.body.toString()).error.code, "BadRequest");
  });

  await check("未知静态文件与 API 方法错误不会泄露实现细节", async () => {
    const missing = await api("/not-found.txt");
    assert.equal(missing.status, 404);
    assertNoSniff(missing);
    assert.equal((await missing.json()).error.code, "NotFound");
    const method = await api("/api/health", { method: "POST" });
    assert.equal(method.status, 405);
    assert.equal(method.headers.get("cache-control"), "no-store");
    assert.equal((await method.json()).error.code, "MethodNotAllowed");
  });
} catch (error) {
  results.push({ name: "测试流程", pass: false, error });
  console.error(`❌ 测试流程\n${error?.stack ?? String(error)}`);
} finally {
  await stopChild();
  await rm(DATA_DIR, { recursive: true, force: true });
}

const failed = results.filter((result) => !result.pass);
console.log(`\n==== Web 回归：${results.length - failed.length}/${results.length} 通过 ====`);
if (failed.length > 0) {
  if (stderr) console.error(`\n---- server stderr 尾部 ----\n${stderr.split("\n").slice(-15).join("\n")}`);
  process.exitCode = 1;
}
