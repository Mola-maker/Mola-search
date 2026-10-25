#!/usr/bin/env node
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { TtlLruCache } from "../dist/core/cache.js";
import { tc3Sign } from "../dist/core/signer.js";
import { WsaError } from "../dist/core/types.js";
import { dryRunPages, validateSearchRequest, WsaClient } from "../dist/core/wsa-client.js";
import { analyzeGaps, aspectCoverage, dedupeMerge } from "../dist/swarm/analyst.js";
import { SwarmEngine } from "../dist/swarm/engine.js";
import { makeAspect, planAspects, refineAspect, variantsFor } from "../dist/swarm/planner.js";
import { HistoryStore } from "../dist/util/text.js";

const TEMP_DIR = mkdtempSync(join(tmpdir(), "ysm-regression-core-"));
const CONFIG_URL = new URL("../dist/config.js", import.meta.url).href;
const TEST_SECRET_ID = "regression-test-id";
const TEST_SECRET_KEY = "regression-test-key";
const tests = [];

function test(name, fn) {
  tests.push({ name, fn });
}

function successBody(query, requestId = "req-ok") {
  return {
    Response: {
      Query: query,
      Pages: [],
      RequestId: requestId,
      Version: "standard",
    },
  };
}

function clientFor(endpoint, overrides = {}) {
  return new WsaClient({
    endpoint,
    secretId: TEST_SECRET_ID,
    secretKey: TEST_SECRET_KEY,
    qps: 1000,
    maxRetries: 0,
    timeoutMs: 2000,
    ...overrides,
  });
}

function assertWsaError(code, status) {
  return (error) => {
    assert.ok(error instanceof WsaError, `预期 WsaError，实际为 ${error}`);
    assert.equal(error.code, code);
    if (status !== undefined) assert.equal(error.httpStatus, status);
    return true;
  };
}

async function withHttpStub(handler, fn) {
  const requests = [];
  const server = createServer(async (req, res) => {
    try {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const text = Buffer.concat(chunks).toString("utf8");
      let body = text;
      if (text) {
        try {
          body = JSON.parse(text);
        } catch {
        }
      }
      const record = { method: req.method, url: req.url, headers: req.headers, body };
      requests.push(record);
      const reply = await handler(record, requests.length, req, res);
      if (!reply || res.writableEnded) return;
      res.writeHead(reply.status ?? 200, { "content-type": "application/json" });
      res.end(JSON.stringify(reply.body ?? successBody(String(record.body?.Query ?? ""))));
    } catch (error) {
      if (!res.writableEnded) {
        res.writeHead(500, { "content-type": "application/json" });
        res.end(JSON.stringify({ error: String(error) }));
      }
    }
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert.ok(address && typeof address === "object");
  const endpoint = `http://127.0.0.1:${address.port}/`;
  try {
    return await fn({ endpoint, requests });
  } finally {
    await new Promise((resolve) => {
      server.close(resolve);
      server.closeAllConnections();
    });
  }
}

test("TC3 签名时间戳与 X-TC-Timestamp 一致", async () => {
  await withHttpStub(
    (request) => ({ body: successBody(request.body.Query, "req-tc3") }),
    async ({ endpoint, requests }) => {
      const before = Math.floor(Date.now() / 1000);
      const result = await clientFor(endpoint).search({ query: "tc3 timestamp", cnt: 10 });
      const after = Math.floor(Date.now() / 1000);
      assert.equal(result.response.requestId, "req-tc3");
      assert.equal(requests.length, 1);
      const request = requests[0];
      const timestamp = Number(request.headers["x-tc-timestamp"]);
      assert.ok(Number.isInteger(timestamp));
      assert.ok(timestamp >= before && timestamp <= after, `${timestamp} 不在 ${before}..${after}`);
      assert.equal(request.headers["x-tc-action"], "SearchPro");
      assert.equal(request.headers["x-tc-version"], "2025-05-08");
      assert.equal(request.headers.host, new URL(endpoint).host);
      const payload = JSON.stringify(request.body);
      const expected = tc3Sign({
        secretId: TEST_SECRET_ID,
        secretKey: TEST_SECRET_KEY,
        service: "wsa",
        host: new URL(endpoint).host,
        action: "SearchPro",
        version: "2025-05-08",
        payload,
        timestamp,
      });
      assert.equal(request.headers.authorization, expected.authorization);
      assert.match(request.headers.authorization, new RegExp(`Credential=${TEST_SECRET_ID}/${expected.date}/wsa/tc3_request`));
    },
  );
});

test("HTTP 503 不缓存，后续成功响应才缓存", async () => {
  await withHttpStub(
    (request, count) => count === 1
      ? {
          status: 503,
          body: { Response: { Error: { Code: "InternalError", Message: "stub unavailable" }, RequestId: "req-503" } },
        }
      : { body: successBody(request.body.Query, "req-after-503") },
    async ({ endpoint, requests }) => {
      const client = clientFor(endpoint);
      const query = { query: "503 cache regression", cnt: 10 };
      await assert.rejects(client.search(query), assertWsaError("InternalError", 503));
      assert.equal(requests.length, 1);
      const second = await client.search(query);
      assert.equal(second.response.requestId, "req-after-503");
      assert.equal(second.stats.cacheHit, false);
      assert.equal(requests.length, 2);
      const third = await client.search(query);
      assert.equal(third.stats.cacheHit, true);
      assert.equal(third.stats.latencyMs, 0);
      assert.equal(requests.length, 2);
    },
  );
});

test("缺少 Response 的 200 响应不缓存", async () => {
  await withHttpStub(
    (request, count) => count === 1
      ? { body: { RequestId: "missing-response" } }
      : { body: successBody(request.body.Query, "req-after-malformed") },
    async ({ endpoint, requests }) => {
      const client = clientFor(endpoint);
      const query = { query: "missing Response cache regression" };
      await assert.rejects(client.search(query), assertWsaError("InternalError", 200));
      const second = await client.search(query);
      assert.equal(second.response.requestId, "req-after-malformed");
      assert.equal(second.stats.cacheHit, false);
      assert.equal(requests.length, 2);
      const cached = await client.search(query);
      assert.equal(cached.stats.cacheHit, true);
      assert.equal(requests.length, 2);
    },
  );
});

test("Freshness 接受并透传 d/m/y，拒绝越界值", async () => {
  await withHttpStub(
    (request) => ({ body: successBody(request.body.Query, `req-${request.body.Freshness}`) }),
    async ({ endpoint, requests }) => {
      const client = clientFor(endpoint);
      for (const freshness of ["d", "m", "y"]) {
        validateSearchRequest({ query: `freshness ${freshness}`, freshness });
        const result = await client.search({ query: `freshness ${freshness}`, freshness });
        assert.equal(result.response.requestId, `req-${freshness}`);
      }
      assert.deepEqual(requests.map((request) => request.body.Freshness), ["d", "m", "y"]);
      for (const freshness of ["d0", "d31", "m0", "m13", "y0", "y6", "D7"]) {
        assert.throws(() => validateSearchRequest({ query: "invalid freshness", freshness }), assertWsaError("InvalidParameter"));
      }
      assert.equal(requests.length, 3);
    },
  );
});

test("TTL/LRU 缓存同键更新、淘汰与过期", async () => {
  const cache = new TtlLruCache(2, 1000);
  cache.set("a", "old");
  cache.set("b", "bee");
  cache.set("a", "new");
  assert.equal(cache.size, 2);
  assert.equal(cache.get("a"), "new");
  cache.set("c", "see");
  assert.equal(cache.get("b"), undefined);
  assert.equal(cache.get("a"), "new");
  assert.equal(cache.get("c"), "see");
  const expiring = new TtlLruCache(1, 5);
  expiring.set("short", 1);
  await delay(20);
  assert.equal(expiring.get("short"), undefined);
  assert.equal(expiring.size, 0);
});

test("WsaClient 拒绝非法数值与非法搜索参数", () => {
  const invalidOptions = [
    { qps: 0 },
    { qps: -1 },
    { qps: Number.NaN },
    { qps: Number.POSITIVE_INFINITY },
    { timeoutMs: 0 },
    { timeoutMs: -1 },
    { timeoutMs: Number.NaN },
    { maxRetries: -1 },
    { maxRetries: 1.5 },
    { maxRetries: Number.NaN },
  ];
  for (const options of invalidOptions) {
    assert.throws(() => new WsaClient({ dryRun: true, ...options }), assertWsaError("InvalidParameter"));
  }
  for (const request of [
    { query: "x", cnt: 17 },
    { query: "x", cnt: Number.NaN },
    { query: "x", mode: Number.NaN },
    { query: "x", mode: 3 },
    { query: "x".repeat(101) },
  ]) {
    assert.throws(() => validateSearchRequest(request), assertWsaError("InvalidParameter"));
  }
  assert.doesNotThrow(() => validateSearchRequest({ query: "x".repeat(100), cnt: 10, mode: 0 }));
});

test("配置加载拒绝非法数值且不读取项目环境文件", () => {
  const cwd = mkdtempSync(join(TEMP_DIR, "config-"));
  const cases = [
    ["YUANBAO_QPS", "0"],
    ["YUANBAO_QPS", "-1"],
    ["YUANBAO_QPS", "NaN"],
    ["YUANBAO_QPS", "Infinity"],
    ["YUANBAO_QPS", ""],
    ["YUANBAO_TIMEOUT_MS", "0"],
    ["YUANBAO_TIMEOUT_MS", "1.5"],
    ["YUANBAO_TIMEOUT_MS", "NaN"],
    ["YUANBAO_MAX_RETRIES", "-1"],
    ["YUANBAO_MAX_RETRIES", "1.5"],
    ["YUANBAO_MAX_RETRIES", "Infinity"],
  ];
  const script = `
    import { loadConfig } from ${JSON.stringify(CONFIG_URL)};
    const cases = ${JSON.stringify(cases)};
    const keys = ["YUANBAO_QPS", "YUANBAO_TIMEOUT_MS", "YUANBAO_MAX_RETRIES"];
    const failures = [];
    for (const [key, value] of cases) {
      for (const name of keys) delete process.env[name];
      process.env[key] = value;
      try {
        loadConfig(process.cwd());
        failures.push(key + "=" + JSON.stringify(value) + " 未拒绝");
      } catch (error) {
        if (!String(error?.message).includes("配置 " + key)) failures.push(String(error?.stack ?? error));
      }
    }
    for (const key of keys) delete process.env[key];
    const defaults = loadConfig(process.cwd());
    if (defaults.qps !== 5 || defaults.timeoutMs !== 15000 || defaults.maxRetries !== 2) failures.push("默认数值异常");
    if (failures.length) {
      console.error(failures.join("\\n"));
      process.exit(1);
    }
  `;
  const env = {};
  for (const key of ["PATH", "SystemRoot", "SystemDrive", "COMSPEC"]) {
    if (process.env[key] !== undefined) env[key] = process.env[key];
  }
  const result = spawnSync(process.execPath, ["--input-type=module", "--eval", script], {
    cwd,
    env,
    encoding: "utf8",
    timeout: 15000,
  });
  assert.equal(result.status, 0, `子进程退出码 ${result.status}\n${result.stdout}\n${result.stderr}\n${result.error ?? ""}`);
});

test("RequestLimitExceeded 按退避策略重试并返回 attempts", async () => {
  await withHttpStub(
    (request, count) => count < 3
      ? {
          status: 429,
          body: { Response: { Error: { Code: "RequestLimitExceeded", Message: "stub limited" }, RequestId: `req-limit-${count}` } },
        }
      : { body: successBody(request.body.Query, "req-after-retry") },
    async ({ endpoint, requests }) => {
      const started = Date.now();
      const result = await clientFor(endpoint, { maxRetries: 2 }).search({ query: "retry regression" });
      const elapsed = Date.now() - started;
      assert.equal(result.response.requestId, "req-after-retry");
      assert.equal(result.stats.attempts, 3);
      assert.equal(result.stats.cacheHit, false);
      assert.equal(requests.length, 3);
      assert.ok(elapsed >= 750, `两次退避应至少约 900ms，实际 ${elapsed}ms`);
    },
  );
});

test("令牌桶在初始突发额度耗尽后限制请求速率", async () => {
  await withHttpStub(
    (request) => ({ body: successBody(request.body.Query) }),
    async ({ endpoint, requests }) => {
      const client = clientFor(endpoint, { qps: 10 });
      const started = Date.now();
      for (let i = 0; i < 12; i += 1) {
        await client.search({ query: `rate-limit-${i}` });
      }
      const elapsed = Date.now() - started;
      assert.equal(requests.length, 12);
      assert.ok(elapsed >= 120, `12 次请求/QPS=10 应触发限流，实际 ${elapsed}ms`);
    },
  );
});

test("预取消与干跑搜索在发请求前终止", async () => {
  const client = new WsaClient({ dryRun: true });
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(client.search({ query: "cancel before start" }, controller.signal), assertWsaError("RequestCancelled"));
  const cached = await client.search({ query: "dry run remains usable" });
  assert.equal(cached.stats.cacheHit, false);
  assert.ok(dryRunPages({ query: "fixture" }).length > 0);
});

test("进行中请求取消后映射 RequestCancelled 且不重试", async () => {
  await withHttpStub(
    () => new Promise(() => {}),
    async ({ endpoint, requests }) => {
      const client = clientFor(endpoint, { maxRetries: 2, timeoutMs: 5000 });
      const controller = new AbortController();
      const pending = client.search({ query: "cancel in flight" }, controller.signal);
      const rejection = assert.rejects(pending, assertWsaError("RequestCancelled"));
      const deadline = Date.now() + 1500;
      while (requests.length === 0 && Date.now() < deadline) await delay(5);
      assert.equal(requests.length, 1, "本地桩未收到请求");
      controller.abort();
      await rejection;
      await delay(30);
      assert.equal(requests.length, 1);
    },
  );
});

test("Planner 限制查询为 100 UTF-16 单元且不留下半个代理对", () => {
  const profile = {
    name: "planner-regression",
    strategy: "comprehensive",
    maxRounds: 2,
    maxSubqueries: 5,
    searchersPerQuery: 3,
    coverageThreshold: 0.75,
    cnt: 20,
  };
  const aspects = planAspects("原".repeat(140), profile);
  assert.ok(aspects.length > 1);
  for (const aspect of aspects) {
    assert.ok(aspect.query.length <= 100, `${aspect.id}: ${aspect.query.length}`);
    assert.doesNotThrow(() => validateSearchRequest({ query: aspect.query }));
    for (const variant of variantsFor(aspect, profile, 0, {})) {
      assert.ok(variant.query.length <= 100);
    }
  }
  const boundary = makeAspect("boundary", "边界", `${"a".repeat(99)}😀tail`);
  assert.equal(boundary.query, "a".repeat(99));
  const refined = refineAspect(makeAspect("long", "长查询", "起".repeat(98)), [], 1);
  assert.ok(refined);
  assert.ok(refined.query.length <= 100);
  assert.ok(!/[\uD800-\uDBFF]$/.test(refined.query));
});

test("Analyst 覆盖度按方面隔离且盲区只属于原方面", () => {
  const alpha = makeAspect("a-alpha", "Alpha", "alpha");
  const beta = makeAspect("a-beta", "Beta", "beta");
  const pages = [
    { title: "alpha", content: "alpha", passage: "alpha", url: "https://example.test/alpha", aspects: ["a-alpha"] },
  ];
  assert.equal(aspectCoverage(alpha, pages), 1);
  assert.equal(aspectCoverage(beta, pages), 0);
  const gap = analyzeGaps([alpha, beta], pages, 0.5);
  assert.deepEqual(gap.coverage.map((item) => [item.aspectId, item.coverage]), [["a-alpha", 1], ["a-beta", 0]]);
  assert.deepEqual(gap.weakAspects.map((aspect) => aspect.id), ["a-beta"]);
  assert.deepEqual(gap.missingTerms.get("a-beta"), ["beta"]);
  assert.equal(gap.missingTerms.has("a-alpha"), false);
  assert.equal(gap.overall, 0.5);
});

test("Analyst 从带方面标记的结果提取新线索并排除原查询词", () => {
  const aspect = makeAspect("a-quantum", "量子计算", "quantum computing roadmap");
  const pages = [
    {
      title: "Quantum computing with superconducting qubits",
      passage: "Superconducting qubits enable error correction",
      url: "https://example.test/qubits-1",
      aspects: ["a-quantum"],
    },
    {
      title: "Quantum computing qubits scale",
      passage: "Superconducting qubits and error correction",
      url: "https://example.test/qubits-2",
      aspects: ["a-quantum"],
    },
    {
      title: "Quantum computing unrelatedclue",
      passage: "unrelatedclue should stay isolated",
      url: "https://example.test/other-aspect",
      aspects: ["a-other"],
    },
  ];
  const gap = analyzeGaps([aspect], pages, 0.9);
  const terms = gap.refinementTerms.get("a-quantum");
  assert.ok(terms);
  assert.ok(terms.includes("qubits"), `缺少结果新线索 qubits：${terms.join(", ")}`);
  assert.ok(terms.includes("superconducting"), `缺少高频结果线索 superconducting：${terms.join(", ")}`);
  for (const originalTerm of ["quantum", "computing", "roadmap"]) {
    assert.equal(terms.includes(originalTerm), false, `原查询词 ${originalTerm} 不应进入 refinementTerms`);
  }
  assert.equal(terms.includes("unrelatedclue"), false);
});

test("Analyst URL/标题去重合并轨迹并保护不同 ID", () => {
  const merged = dedupeMerge([
    {
      url: "https://www.Example.test/article/?b=2&utm_source=stub&a=1#first",
      title: "相同标题",
      content: "低分内容",
      score: 0.2,
      aspects: ["a0"],
      round: 1,
    },
    {
      url: "https://example.test/article?a=1&b=2",
      title: "相同标题",
      content: "高分内容",
      score: 0.9,
      aspects: ["a1"],
      round: 0,
    },
  ]);
  assert.equal(merged.length, 1);
  assert.equal(merged[0].score, 0.9);
  assert.equal(merged[0].content, "高分内容");
  assert.deepEqual(merged[0].aspects, ["a0", "a1"]);
  assert.equal(merged[0].round, 0);
  const titleTwins = dedupeMerge([
    { url: "https://example.test/first", title: "完全相同标题", aspects: ["a0"] },
    { url: "https://example.test/second", title: "完全相同标题", aspects: ["a1"] },
  ]);
  assert.equal(titleTwins.length, 1);
  assert.deepEqual(titleTwins[0].aspects, ["a0", "a1"]);
  const distinctIds = dedupeMerge([
    { url: "https://example.test/item?id=1", title: "完全相同标题" },
    { url: "https://example.test/item?id=2", title: "完全相同标题" },
  ]);
  assert.equal(distinctIds.length, 2);
});

test("迭代补盲映射原始方面且 raw/unique/cache/调用统计准确", async () => {
  const searches = [];
  const client = {
    async search(request) {
      searches.push(request);
      if (searches.length === 1) {
        return {
          response: {
            query: request.query,
            requestId: "round-0",
            pages: [
              { title: "完全无关的信息 qubits", content: "完全无关的信息 qubits", url: "https://example.test/duplicate?utm_source=stub", score: 0.1 },
              { title: "完全无关的信息 qubits", content: "完全无关的信息 qubits", url: "https://example.test/duplicate", score: 0.2 },
            ],
          },
          stats: { latencyMs: 1, attempts: 1, cacheHit: false },
        };
      }
      return {
        response: {
          query: request.query,
          requestId: "round-1",
          pages: [
            { title: request.query, content: request.query, passage: request.query, url: "https://example.test/refined", score: 0.9 },
          ],
        },
        stats: { latencyMs: 0, attempts: 1, cacheHit: true },
      };
    },
  };
  const historyDir = mkdtempSync(join(TEMP_DIR, "history-"));
  const history = new HistoryStore(historyDir);
  const engine = new SwarmEngine(client, history);
  const profile = {
    name: "iteration-regression",
    strategy: "quick",
    maxRounds: 2,
    maxSubqueries: 1,
    searchersPerQuery: 1,
    coverageThreshold: 0.9,
    cnt: 10,
  };
  const { report, historyId } = await engine.run({ query: "原始主题", profile });
  assert.equal(searches.length, 2);
  assert.notEqual(searches[1].query, searches[0].query);
  assert.ok(searches[1].query.startsWith("原始主题 "), `下一轮 variant.query 未保留原提示词核心：${searches[1].query}`);
  assert.ok(searches[1].query.includes("qubits"), `下一轮 variant.query 未包含结果线索 qubits：${searches[1].query}`);
  assert.ok(report.iterationTrace[1].queries[0].includes("qubits"));
  assert.equal(report.roundsExecuted, 2);
  assert.equal(report.converged, true);
  assert.deepEqual(report.iterationTrace.map((trace) => [trace.hits, trace.newUnique]), [[2, 1], [1, 1]]);
  assert.equal(report.stats.totalApiCalls, 2);
  assert.equal(report.stats.rawHits, 3);
  assert.equal(report.stats.uniqueHits, 2);
  assert.equal(report.stats.cacheHits, 1);
  assert.equal(report.coverage.length, 1);
  assert.equal(report.coverage[0].aspectId, "a0");
  assert.equal(report.coverage[0].coverage, 1);
  assert.equal(report.overallCoverage, 1);
  const originalFinding = report.findings.find((finding) => finding.aspectId === "a0");
  assert.ok(originalFinding);
  assert.ok(originalFinding.topPages.some((page) => page.url === "https://example.test/refined"));
  assert.ok(historyId);
  const saved = history.get(historyId);
  assert.equal(saved.totalCalls, 2);
  assert.equal(saved.totalHits, 3);
  assert.equal(saved.uniqueHits, 2);
  assert.equal(saved.rounds, 2);
});

let passed = 0;
const failures = [];
try {
  for (const { name, fn } of tests) {
    try {
      await fn();
      passed += 1;
      console.log(`✓ ${name}`);
    } catch (error) {
      failures.push({ name, error });
      console.error(`✗ ${name}`);
      console.error(error?.stack ?? error);
    }
  }
} finally {
  rmSync(TEMP_DIR, { recursive: true, force: true });
}

console.log(`\n核心回归：${passed}/${tests.length} 通过`);
if (failures.length > 0) {
  console.error(`失败 ${failures.length} 项：${failures.map((failure) => failure.name).join("；")}`);
  process.exitCode = 1;
}
