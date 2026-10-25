import { createReadStream } from "node:fs";
import { stat } from "node:fs/promises";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { extname, resolve, sep } from "node:path";
import { pipeline } from "node:stream/promises";
import { fileURLToPath } from "node:url";
import "dotenv/config";
import { loadConfig, type PluginConfig, type SwarmProfile } from "../config.js";
import { validateSearchRequest, WsaClient } from "../core/wsa-client.js";
import { WsaError, type SearchPage, type SearchRequest } from "../core/types.js";
import { SwarmEngine } from "../swarm/engine.js";
import type { SynthReport } from "../swarm/synthesizer.js";
import { log } from "../util/log.js";
import { HistoryStore } from "../util/text.js";

const BODY_LIMIT = 32 * 1024;
const STATIC_ROOT = fileURLToPath(new URL("../../dist-web/", import.meta.url));
const NDJSON = "application/x-ndjson; charset=utf-8";

type Stage = "planning" | "searching" | "analyzing" | "synthesizing";

interface SearchBody {
  query: string;
  kind: "single" | "swarm";
  request: SearchRequest;
  profile?: string;
}

interface WebDependencies {
  config: PluginConfig;
  client: WsaClient;
  engine: SwarmEngine;
}

class HttpError extends Error {
  constructor(
    public readonly status: number,
    message: string,
    public readonly code = "BadRequest",
  ) {
    super(message);
    this.name = "HttpError";
  }
}

export function createWebServer(dependencies?: Partial<WebDependencies>): Server {
  const config = dependencies?.config ?? loadConfig();
  const client =
    dependencies?.client ??
    new WsaClient({
      secretId: config.secretId,
      secretKey: config.secretKey,
      endpoint: config.endpoint,
      qps: config.qps,
      timeoutMs: config.timeoutMs,
      maxRetries: config.maxRetries,
      dryRun: config.dryRun,
    });
  const history = new HistoryStore(config.dataDir);
  const engine = dependencies?.engine ?? new SwarmEngine(client, history);
  const deps: WebDependencies = { config, client, engine };

  return createServer((req, res) => {
    void handleRequest(req, res, deps).catch((err: unknown) => sendError(req, res, err));
  });
}

export function startWebServer(port = readPort()): Server {
  const server = createWebServer();
  server.listen(port, "127.0.0.1", () => {
    log(`Web 服务已启动：http://127.0.0.1:${port}`);
  });
  return server;
}

async function handleRequest(req: IncomingMessage, res: ServerResponse, deps: WebDependencies): Promise<void> {
  const url = new URL(req.url ?? "/", "http://127.0.0.1");
  const isApi = url.pathname.startsWith("/api/");
  if (isApi) setNoCache(res);

  if (url.pathname === "/api/health") {
    if (req.method !== "GET") throw new HttpError(405, "仅支持 GET", "MethodNotAllowed");
    res.writeHead(200, { "Content-Type": "application/json; charset=utf-8" });
    res.end(JSON.stringify({ ok: true, dryRun: deps.config.dryRun }));
    return;
  }

  if (url.pathname === "/api/search") {
    if (req.method !== "POST") throw new HttpError(405, "仅支持 POST", "MethodNotAllowed");
    await handleSearch(req, res, deps);
    return;
  }

  if (isApi) throw new HttpError(404, "接口不存在", "NotFound");
  await serveStatic(req, res, url.pathname);
}

async function handleSearch(req: IncomingMessage, res: ServerResponse, deps: WebDependencies): Promise<void> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  const onClose = () => {
    if (!res.writableEnded) controller.abort();
  };
  req.once("aborted", abort);
  res.once("close", onClose);

  try {
    const body = parseSearchBody(await readJsonBody(req));
    const profile = body.kind === "swarm" ? resolveProfile(deps.config, body.profile) : undefined;
    if (controller.signal.aborted) return;

    res.writeHead(200, {
      "Content-Type": NDJSON,
      "Cache-Control": "no-store",
      Pragma: "no-cache",
      Expires: "0",
      "X-Content-Type-Options": "nosniff",
    });

    const emit = (event: Record<string, unknown>) => {
      if (!res.destroyed && !res.writableEnded) res.write(`${JSON.stringify(event)}\n`);
    };
    const stage = (name: Stage, message: string, round?: number) => {
      emit({ type: "stage", stage: name, message, ...(round === undefined ? {} : { round }) });
    };

    if (body.kind === "single") {
      stage("searching", "正在执行元宝搜索");
      const { response, stats } = await deps.client.search(body.request, controller.signal);
      emit({
        type: "result",
        result: {
          kind: "single",
          query: body.query,
          pages: response.pages,
          requestId: response.requestId,
          stats,
        },
      });
    } else {
      let currentRound = 0;
      stage("planning", `正在使用 ${profile!.name} 规划搜索`);
      const { report, historyId } = await deps.engine.run({
        query: body.query,
        profile: profile!,
        overrides: {
          mode: body.request.mode,
          cnt: body.request.cnt,
          industry: body.request.industry,
          freshness: body.request.freshness,
        },
        signal: controller.signal,
        onProgress: (progress) => {
          if (progress.message === "完成") return;
          if (progress.message === "汇总报告生成中") {
            if (currentRound > 0) stage("analyzing", "正在分析搜索结果与覆盖盲区", currentRound);
            stage("synthesizing", progress.message);
            return;
          }
          const round = progress.round + 1;
          if (currentRound > 0) stage("analyzing", "正在分析搜索结果与覆盖盲区", currentRound);
          currentRound = round;
          stage("searching", progress.message, round);
        },
      });
      emit({
        type: "result",
        result: {
          kind: "swarm",
          query: body.query,
          pages: reportPages(report),
          historyId,
          report,
          stats: report.stats,
        },
      });
    }
    res.end();
  } catch (err) {
    if (controller.signal.aborted) {
      if (!res.destroyed && !res.writableEnded) res.end();
      return;
    }
    throw err;
  } finally {
    req.off("aborted", abort);
    res.off("close", onClose);
  }
}

function parseSearchBody(value: unknown): SearchBody {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new HttpError(400, "请求体必须是 JSON 对象", "InvalidParameter");
  }
  const body = value as Record<string, unknown>;
  const allowed = new Set(["query", "kind", "mode", "cnt", "industry", "freshness", "profile"]);
  const unknown = Object.keys(body).find((key) => !allowed.has(key));
  if (unknown) throw new HttpError(400, `不支持的字段：${unknown}`, "InvalidParameter");
  if (typeof body.query !== "string" || !body.query.trim()) {
    throw new HttpError(400, "query 必须是非空字符串", "InvalidParameter");
  }
  if (body.kind !== "single" && body.kind !== "swarm") {
    throw new HttpError(400, "kind 仅支持 single 或 swarm", "InvalidParameter");
  }
  if (body.mode !== undefined && (typeof body.mode !== "number" || !Number.isInteger(body.mode))) {
    throw new HttpError(400, "mode 必须是整数", "InvalidParameter");
  }
  if (body.cnt !== undefined && (typeof body.cnt !== "number" || !Number.isInteger(body.cnt))) {
    throw new HttpError(400, "cnt 必须是整数", "InvalidParameter");
  }
  if (body.industry !== undefined && typeof body.industry !== "string") {
    throw new HttpError(400, "industry 必须是字符串", "InvalidParameter");
  }
  if (body.freshness !== undefined && typeof body.freshness !== "string") {
    throw new HttpError(400, "freshness 必须是字符串", "InvalidParameter");
  }
  if (body.profile !== undefined && (typeof body.profile !== "string" || !body.profile.trim())) {
    throw new HttpError(400, "profile 必须是非空字符串", "InvalidParameter");
  }

  const query = body.query.trim();
  const request: SearchRequest = {
    query,
    mode: body.mode as number | undefined,
    cnt: body.cnt as number | undefined,
    industry: body.industry as string | undefined,
    freshness: body.freshness as string | undefined,
  };
  validateSearchRequest(request);
  return { query, kind: body.kind, request, profile: body.profile as string | undefined };
}

function resolveProfile(config: PluginConfig, name?: string): SwarmProfile {
  const wanted = name ?? "deep-research";
  const profile = config.profiles.find((item) => item.name === wanted);
  if (!profile) throw new HttpError(400, `未知 Profile：${wanted}`, "InvalidParameter");
  return profile;
}

function reportPages(report: SynthReport): SearchPage[] {
  const pages: SearchPage[] = [];
  const seen = new Set<string>();
  for (const finding of report.findings) {
    for (const page of finding.topPages) {
      if (page.url) {
        if (seen.has(page.url)) continue;
        seen.add(page.url);
      }
      pages.push({
        title: page.title,
        url: page.url,
        site: page.site,
        date: page.date,
        score: page.score,
        authority_level: page.authority ?? undefined,
        passage: page.snippet,
        aspects: [finding.aspectId],
      });
    }
  }
  return pages;
}

function readJsonBody(req: IncomingMessage): Promise<unknown> {
  const contentType = req.headers["content-type"]?.split(";", 1)[0].trim().toLowerCase();
  if (contentType !== "application/json") {
    req.resume();
    return Promise.reject(new HttpError(415, "Content-Type 必须为 application/json", "UnsupportedMediaType"));
  }
  const length = req.headers["content-length"];
  if (length !== undefined) {
    const size = Number(length);
    if (!Number.isSafeInteger(size) || size < 0) {
      req.resume();
      return Promise.reject(new HttpError(400, "Content-Length 非法", "BadRequest"));
    }
    if (size > BODY_LIMIT) {
      req.resume();
      return Promise.reject(new HttpError(413, "请求体不能超过 32KB", "PayloadTooLarge"));
    }
  }

  return new Promise((resolveBody, rejectBody) => {
    const chunks: Buffer[] = [];
    let size = 0;
    let settled = false;

    const cleanup = () => {
      req.off("data", onData);
      req.off("end", onEnd);
      req.off("error", onError);
      req.off("aborted", onAborted);
    };
    const fail = (err: Error) => {
      if (settled) return;
      settled = true;
      cleanup();
      req.resume();
      rejectBody(err);
    };
    const onData = (chunk: Buffer | string) => {
      const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
      size += buffer.length;
      if (size > BODY_LIMIT) {
        fail(new HttpError(413, "请求体不能超过 32KB", "PayloadTooLarge"));
        return;
      }
      chunks.push(buffer);
    };
    const onEnd = () => {
      if (settled) return;
      settled = true;
      cleanup();
      try {
        const text = Buffer.concat(chunks).toString("utf8");
        if (!text.trim()) throw new SyntaxError("empty body");
        resolveBody(JSON.parse(text));
      } catch {
        rejectBody(new HttpError(400, "请求体不是有效 JSON", "InvalidJson"));
      }
    };
    const onError = (err: Error) => fail(err);
    const onAborted = () => fail(new HttpError(400, "客户端中断请求", "ClientDisconnected"));

    req.on("data", onData);
    req.once("end", onEnd);
    req.once("error", onError);
    req.once("aborted", onAborted);
  });
}

async function serveStatic(req: IncomingMessage, res: ServerResponse, pathname: string): Promise<void> {
  if (req.method !== "GET" && req.method !== "HEAD") {
    throw new HttpError(405, "静态资源仅支持 GET/HEAD", "MethodNotAllowed");
  }

  let decoded: string;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    throw new HttpError(400, "URL 编码非法", "BadRequest");
  }
  if (decoded.includes("\0")) throw new HttpError(403, "路径非法", "Forbidden");

  const root = resolve(STATIC_ROOT);
  const relative = decoded.replace(/^\/+/, "").replace(/\\/g, "/");
  if (relative.split("/").includes("..")) throw new HttpError(403, "禁止路径穿越", "Forbidden");
  let filePath = resolve(root, relative || "index.html");
  if (filePath !== root && !filePath.startsWith(root + sep)) {
    throw new HttpError(403, "禁止路径穿越", "Forbidden");
  }

  try {
    let info = await stat(filePath);
    if (info.isDirectory()) {
      filePath = resolve(filePath, "index.html");
      info = await stat(filePath);
    }
    if (!info.isFile()) throw new HttpError(404, "文件不存在", "NotFound");

    res.writeHead(200, {
      "Content-Type": mimeType(filePath),
      "Content-Length": info.size,
      "Cache-Control": extname(filePath) === ".html" ? "no-cache" : "public, max-age=3600",
      "X-Content-Type-Options": "nosniff",
    });
    if (req.method === "HEAD") {
      res.end();
      return;
    }
    await pipeline(createReadStream(filePath), res);
  } catch (err) {
    if (err instanceof HttpError) throw err;
    if ((err as NodeJS.ErrnoException).code === "ENOENT" || (err as NodeJS.ErrnoException).code === "ENOTDIR") {
      throw new HttpError(404, "文件不存在", "NotFound");
    }
    throw err;
  }
}

function mimeType(path: string): string {
  const types: Record<string, string> = {
    ".html": "text/html; charset=utf-8",
    ".js": "text/javascript; charset=utf-8",
    ".mjs": "text/javascript; charset=utf-8",
    ".css": "text/css; charset=utf-8",
    ".json": "application/json; charset=utf-8",
    ".svg": "image/svg+xml",
    ".png": "image/png",
    ".jpg": "image/jpeg",
    ".jpeg": "image/jpeg",
    ".gif": "image/gif",
    ".webp": "image/webp",
    ".ico": "image/x-icon",
    ".wasm": "application/wasm",
    ".woff2": "font/woff2",
    ".txt": "text/plain; charset=utf-8",
    ".map": "application/json; charset=utf-8",
  };
  return types[extname(path).toLowerCase()] ?? "application/octet-stream";
}

function setNoCache(res: ServerResponse): void {
  res.setHeader("Cache-Control", "no-store");
  res.setHeader("Pragma", "no-cache");
  res.setHeader("Expires", "0");
}

function sendError(req: IncomingMessage, res: ServerResponse, err: unknown): void {
  if (res.destroyed || res.writableEnded) return;
  const isSearch = req.url?.split("?", 1)[0] === "/api/search";
  const status = errorStatus(err);
  const code = err instanceof WsaError || err instanceof HttpError ? err.code : "InternalError";
  const message =
    err instanceof WsaError && err.requestId
      ? `${err.message} (RequestId: ${err.requestId})`
      : err instanceof Error
        ? err.message
        : "服务内部错误";

  if (isSearch) {
    if (!res.headersSent) res.writeHead(status, { "Content-Type": NDJSON });
    res.end(`${JSON.stringify({ type: "error", error: { message, code } })}\n`);
    return;
  }
  if (res.headersSent) {
    res.destroy();
    return;
  }
  res.writeHead(status, { "Content-Type": "application/json; charset=utf-8", "X-Content-Type-Options": "nosniff" });
  res.end(JSON.stringify({ error: { message, code } }));
}

function errorStatus(err: unknown): number {
  if (err instanceof HttpError) return err.status;
  if (err instanceof WsaError && err.code === "InvalidParameter") return 400;
  return 500;
}

function readPort(): number {
  const raw = process.env.PORT ?? "3210";
  const port = Number(raw);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error("PORT 必须是 1 到 65535 之间的整数");
  }
  return port;
}

const isMain = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (isMain) {
  try {
    startWebServer().on("error", (err) => {
      log(`Web 服务启动失败：${err.message}`);
      process.exitCode = 1;
    });
  } catch (err) {
    log(`Web 服务启动失败：${err instanceof Error ? err.message : String(err)}`);
    process.exitCode = 1;
  }
}
