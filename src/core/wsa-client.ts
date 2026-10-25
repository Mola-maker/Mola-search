import { WSA_CONSTRAINTS, WsaError, type SearchPage, type SearchRequest, type SearchResponse, type WsaCallStats } from "./types.js";
import { tc3Sign } from "./signer.js";
import { TtlLruCache } from "./cache.js";

export interface WsaClientOptions {
  secretId: string;
  secretKey: string;
  endpoint?: string;
  /** 每秒最大请求数（令牌桶），默认 5 */
  qps?: number;
  /** 单请求超时 ms，默认 15000 */
  timeoutMs?: number;
  /** 失败重试次数（429/内部错误/网络错误），默认 2 */
  maxRetries?: number;
  /** 干跑模式：不发网络请求，返回确定性夹具数据（用于联调与冒烟测试） */
  dryRun?: boolean;
}

const DEFAULT_ENDPOINT = "https://wsa.tencentcloudapi.com";
const SERVICE = "wsa";
const VERSION = "2025-05-08";
const ACTION = "SearchPro";

/** 令牌桶限流：保护免费额度并规避 RequestLimitExceeded */
class TokenBucket {
  private tokens: number;
  private last = Date.now();
  constructor(private readonly qps: number) {
    this.tokens = qps;
  }
  async take(signal?: AbortSignal): Promise<void> {
    for (;;) {
      throwIfAborted(signal);
      const now = Date.now();
      this.tokens = Math.min(this.qps, this.tokens + ((now - this.last) / 1000) * this.qps);
      this.last = now;
      if (this.tokens >= 1) {
        this.tokens -= 1;
        return;
      }
      await sleep(Math.ceil((1 - this.tokens) * 1000 / this.qps), signal);
    }
  }
}

function cancelledError(): WsaError {
  return new WsaError("请求已取消", "RequestCancelled");
}

function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted) throw cancelledError();
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    throwIfAborted(signal);
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort);
      reject(cancelledError());
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

/** 校验文档约束（全量编码） */
export function validateSearchRequest(req: SearchRequest): void {
  if (!req.query || !req.query.trim()) {
    throw new WsaError("Query 不能为空", "InvalidParameter");
  }
  if (req.query.length > WSA_CONSTRAINTS.maxQueryLen) {
    throw new WsaError(`Query 长度超限（>${WSA_CONSTRAINTS.maxQueryLen}）`, "InvalidParameter");
  }
  if (req.mode !== undefined && !WSA_CONSTRAINTS.modes.includes(req.mode as 0 | 1 | 2)) {
    throw new WsaError(`Mode 仅支持 0(公网)/1(优质权威垂源)/2(混合)，收到 ${req.mode}`, "InvalidParameter");
  }
  if (req.cnt !== undefined && !WSA_CONSTRAINTS.cnt.includes(req.cnt as 10)) {
    throw new WsaError(`Cnt 仅支持 ${WSA_CONSTRAINTS.cnt.join("/")}`, "InvalidParameter");
  }
  if (req.industry !== undefined && !WSA_CONSTRAINTS.industries.includes(req.industry as "gov")) {
    throw new WsaError(`Industry 仅支持 ${WSA_CONSTRAINTS.industries.join("/")}`, "InvalidParameter");
  }
  if (req.freshness !== undefined && !WSA_CONSTRAINTS.freshnessRe.test(req.freshness)) {
    throw new WsaError(
      "Freshness 非法：应为 d / m / y / d[1-30] / m[1-12] / y[1-5] / yyyy-mm-dd / yyyy-mm-dd,yyyy-mm-dd",
      "InvalidParameter",
    );
  }
  if (req.site !== undefined) {
    if (req.site.startsWith("exclude:")) {
      const excluded = req.site.slice("exclude:".length).split("|").filter(Boolean);
      if (excluded.length > WSA_CONSTRAINTS.maxSiteExcludes) {
        throw new WsaError(`exclude: 最多排除 ${WSA_CONSTRAINTS.maxSiteExcludes} 个域名`, "InvalidParameter");
      }
    }
  }
}

/** 干跑夹具：确定性生成，保证冒烟测试无需密钥与外网 */
export function dryRunPages(req: SearchRequest, seed = 0): SearchPage[] {
  const cnt = req.cnt ?? 10;
  const base = [...req.query].reduce((a, c) => a + c.charCodeAt(0), 0) + seed;
  const sites = ["qq.com", "cloud.tencent.com", "gov.cn", "zhihu.com", "github.com"];
  const out: SearchPage[] = [];
  for (let i = 0; i < Math.min(cnt, 10); i++) {
    const q = req.query;
    out.push({
      title: `${q} · ${req.industry ?? "综合"} 报道 ${i + 1}`,
      date: new Date(Date.now() - i * 86400000).toISOString().slice(0, 10),
      url: `https://dryrun.example/${encodeURIComponent(q)}/${i + (base % 97)}`,
      passage: `【dry-run 夹具】关于「${q}」的标准摘要段落 ${i + 1}。`,
      content: `【dry-run 夹具】针对「${q}」的动态摘要：要点 ${i + 1}，模式 ${req.mode ?? 0}，时效 ${req.freshness ?? "无"}。`,
      site: sites[(i + base) % sites.length],
      score: Math.round((0.95 - i * 0.06 + (base % 5) * 0.01) * 100) / 100,
      authority_level: req.mode === 0 ? undefined : 5 - (i % 4),
      pics: [],
      favicon: undefined,
      deeplinks: req.deeplinks ? [{ title: `${q} 延伸阅读`, url: `https://dryrun.example/${i}/more` }] : undefined,
    });
  }
  return out;
}

export class WsaClient {
  private bucket: TokenBucket;
  private cache: TtlLruCache<SearchResponse>;

  constructor(private readonly opts: WsaClientOptions) {
    const qps = opts.qps ?? 5;
    const timeoutMs = opts.timeoutMs ?? 15000;
    const maxRetries = opts.maxRetries ?? 2;
    if (!Number.isFinite(qps) || qps <= 0) {
      throw new WsaError("QPS 必须为正数", "InvalidParameter");
    }
    if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
      throw new WsaError("TimeoutMs 必须为正数", "InvalidParameter");
    }
    if (!Number.isInteger(maxRetries) || maxRetries < 0) {
      throw new WsaError("MaxRetries 必须为非负整数", "InvalidParameter");
    }
    if (!opts.dryRun && (!opts.secretId || !opts.secretKey)) {
      throw new WsaError(
        "缺少腾讯云凭证：请设置 TENCENTCLOUD_SECRET_ID / TENCENTCLOUD_SECRET_KEY（或设 YUANBAO_DRY_RUN=1 使用干跑模式）",
        "UnauthorizedOperation",
      );
    }
    this.bucket = new TokenBucket(qps);
    this.cache = new TtlLruCache<SearchResponse>(256, 10 * 60 * 1000);
  }

  /** 调用 SearchPro：校验 → 缓存 → 限流 → TC3 签名 POST → 重试 → 解析 */
  async search(req: SearchRequest, signal?: AbortSignal): Promise<{ response: SearchResponse; stats: WsaCallStats }> {
    validateSearchRequest(req);
    throwIfAborted(signal);

    const body: Record<string, unknown> = { Query: req.query };
    if (req.mode !== undefined) body.Mode = req.mode;
    if (req.site !== undefined) body.Site = req.site;
    if (req.cnt !== undefined) body.Cnt = req.cnt;
    if (req.industry !== undefined) body.Industry = req.industry;
    if (req.freshness !== undefined) body.Freshness = req.freshness;
    if (req.deeplinks !== undefined) body.Deeplinks = req.deeplinks;
    const payload = JSON.stringify(body);

    const cacheKey = payload;
    const cached = this.cache.get(cacheKey);
    if (cached) {
      return { response: cached, stats: { latencyMs: 0, attempts: 1, cacheHit: true } };
    }

    if (this.opts.dryRun) {
      const resp: SearchResponse = {
        query: req.query,
        pages: dryRunPages(req),
        version: "standard",
        msg: "dry-run fixture",
        requestId: "dryrun-" + Math.abs(hash(payload)).toString(16),
      };
      this.cache.set(cacheKey, resp);
      return { response: resp, stats: { latencyMs: 0, attempts: 1, cacheHit: false } };
    }

    const started = Date.now();
    const maxRetries = this.opts.maxRetries ?? 2;
    let attempt = 0;
    for (;;) {
      attempt += 1;
      await this.bucket.take(signal);
      try {
        const res = await this.postOnce(payload, signal);
        this.cache.set(cacheKey, res);
        return { response: res, stats: { latencyMs: Date.now() - started, attempts: attempt, cacheHit: false } };
      } catch (err) {
        const retryable =
          err instanceof WsaError &&
          (err.code === "RequestLimitExceeded" ||
            err.code === "InternalError" ||
            err.code === "ResourceUnavailable" ||
            err.httpStatus === 429);
        if (!retryable || attempt > maxRetries) throw err;
        await sleep(300 * 2 ** (attempt - 1), signal);
      }
    }
  }

  private async postOnce(payload: string, signal?: AbortSignal): Promise<SearchResponse> {
    throwIfAborted(signal);
    const endpoint = this.opts.endpoint ?? DEFAULT_ENDPOINT;
    const host = new URL(endpoint).host;
    const { authorization, timestamp } = tc3Sign({
      secretId: this.opts.secretId,
      secretKey: this.opts.secretKey,
      service: SERVICE,
      host,
      action: ACTION,
      version: VERSION,
      payload,
    });

    const timeoutMs = this.opts.timeoutMs ?? 15000;
    const ctrl = new AbortController();
    let timedOut = false;
    const onAbort = () => ctrl.abort();
    signal?.addEventListener("abort", onAbort, { once: true });
    const timer = setTimeout(() => {
      timedOut = true;
      ctrl.abort();
    }, timeoutMs);
    let httpStatus = 0;
    try {
      const res = await fetch(endpoint, {
        method: "POST",
        headers: {
          "Content-Type": "application/json; charset=utf-8",
          Host: host,
          "X-TC-Action": ACTION,
          "X-TC-Version": VERSION,
          "X-TC-Timestamp": String(timestamp),
          Authorization: authorization,
        },
        body: payload,
        signal: ctrl.signal,
      });
      httpStatus = res.status;
      let json: unknown;
      try {
        json = await res.json();
      } catch (err) {
        if (!res.ok) {
          throw new WsaError(`元宝搜索 HTTP 请求失败（${res.status}）`, httpErrorCode(res.status), res.status);
        }
        throw err;
      }
      const inner = isRecord(json) && isRecord(json.Response) ? json.Response : undefined;
      if (inner?.Error) {
        const apiError = isRecord(inner.Error) ? inner.Error : {};
        throw new WsaError(
          typeof apiError.Message === "string" ? apiError.Message : "未知接口错误",
          typeof apiError.Code === "string" ? apiError.Code : "InternalError",
          httpStatus,
          typeof inner.RequestId === "string" ? inner.RequestId : undefined,
        );
      }
      if (!res.ok) {
        throw new WsaError(
          `元宝搜索 HTTP 请求失败（${res.status}）`,
          httpErrorCode(res.status),
          res.status,
          typeof inner?.RequestId === "string" ? inner.RequestId : undefined,
        );
      }
      if (!inner || !Array.isArray(inner.Pages) || typeof inner.RequestId !== "string" || !inner.RequestId) {
        throw new WsaError("元宝搜索响应格式非法", "InternalError", httpStatus);
      }
      const pages = inner.Pages.map((page) => safeParsePage(page));
      if (pages.some((page): page is null => page === null)) {
        throw new WsaError("元宝搜索响应包含非法 Pages", "InternalError", httpStatus, inner.RequestId);
      }
      return {
        query: String(inner.Query ?? req0Query(payload)),
        pages: pages as SearchPage[],
        version: String(inner.Version ?? "unknown"),
        msg: typeof inner.Msg === "string" ? inner.Msg : undefined,
        requestId: inner.RequestId,
      };
    } catch (err) {
      if (signal?.aborted) throw cancelledError();
      if (err instanceof WsaError) throw err;
      if (err instanceof Error && err.name === "AbortError" && timedOut) {
        throw new WsaError(`请求元宝搜索超时（>${timeoutMs}ms）`, "ResourceUnavailable", httpStatus);
      }
      throw new WsaError(`网络错误：${err instanceof Error ? err.message : String(err)}`, "InternalError", httpStatus);
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort);
    }
  }
}

function req0Query(payload: string): string {
  try {
    return String(JSON.parse(payload).Query ?? "");
  } catch {
    return "";
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function httpErrorCode(status: number): string {
  if (status === 429) return "RequestLimitExceeded";
  if (status >= 500) return "InternalError";
  return "InvalidResponse";
}

function safeParsePage(raw: unknown): SearchPage | null {
  try {
    const parsed: unknown = typeof raw === "string" ? JSON.parse(raw) : raw;
    return isRecord(parsed) ? parsed as SearchPage : null;
  } catch {
    return null;
  }
}

function hash(s: string): number {
  let h = 0;
  for (const ch of s) h = (h * 31 + ch.charCodeAt(0)) | 0;
  return h;
}
