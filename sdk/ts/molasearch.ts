/**
 * MolaSearch client for the real `magpie web` API.
 *
 * Two ports matter and are not interchangeable: 3425 is magpie's LLM
 * gateway (OpenAI/Anthropic/Gemini compatible); 3430 is `magpie web`, which
 * serves the Detective page and the `/api/*` routes this client calls.
 *
 * Start it with `molasearch.exe web --addr 127.0.0.1:3430`. If
 * `MAGPIE_WEB_KEY` is set, pass it as `webKey`; each request then carries
 * the `magpie_web_<port>` sign-in cookie. This client targets Node 18+ (or
 * any fetch implementation that allows setting a Cookie header); a browser
 * cannot set that header and should use the page itself.
 *
 * Every method maps to a route in internal/gui/detective_api.go or
 * internal/gui/rag_api.go. No method returns sample data.
 */

export const DEFAULT_BASE_URL = "http://127.0.0.1:3430";

export type Verdict = "VERIFIED" | "REFUTED" | "UNVERIFIED" | "ABSTAIN";

export interface Citation {
  title: string;
  url: string;
  domain: string;
  sourceName: string;
  channel: string;
  authorityLevel: number;
  publishedAt: string;
  snippet: string;
}

export interface TrajectoryStep {
  originAgent: string;
  modeId: string;
  gateway: string;
  params: string;
  startOffsetMs: number;
  endOffsetMs: number;
  durationMs: number;
  pctRedact: number;
  pctPlan: number;
  pctLocal: number;
  pctNet: number;
  pctRerank: number;
  timingBreakdown: string;
  rawHits: number;
  dedupedHits: number;
  collapsedSpam: number;
  returnSummary: string;
  topUrl: string;
  topDomain: string;
}

export interface RedactAudit {
  maskedQuery: string;
  maskedCount: number;
  placeholders: string[];
  complianceStatus: string;
  blockedReason?: string;
}

export interface Investigation {
  traceId: string;
  query: string;
  modeId: string;
  modeName: string;
  originAgent: string;
  verdict: Verdict;
  trustScore: number;
  primaryEngine: string;
  activeModeBadge: string;
  modeNote: string;
  executiveSummary: string;
  redactAudit: RedactAudit;
  totalLatencyMs: number;
  /** How many merged citations each search provider contributed. */
  channelHits: Record<string, number>;
  collapsedSpamCount: number;
  markdownBriefing?: string;
  agentEnvelopeXml?: string;
  citations: Citation[];
  claimMatrix: Array<{ claim: string; fact: string; status: string; sourceUrl: string; citeIndex: number }>;
  temporalTree: Array<{ timestamp: string; stage: string; edgeType: string; title: string; detail: string; url: string }>;
  multimodalCards: Array<Record<string, unknown>>;
  debate: { pro: string[]; con: string[]; judgeSummary: string };
  trajectory: TrajectoryStep[];
  fusionNetwork: Record<string, unknown>;
  abstainReason?: string;
  noPersist: boolean;
  generatedAt: string;
}

export interface InvestigateRequest {
  query: string;
  modeId?: string;
  debate?: boolean;
  enableCU?: boolean;
  imageBase64?: string;
  imageName?: string;
  pageUrl?: string;
}

export interface MemoryRecord {
  memoryId?: string;
  topic: string;
  claim?: string;
  evidenceText: string;
  verdict?: string;
  relationEdge?: string;
  validFrom?: string;
  invalidAt?: string;
  sourceUrl?: string;
  sourceDomain?: string;
  authorityLevel?: number;
  embeddingModel?: string;
}

export interface StakeoutJob {
  id?: string;
  title?: string;
  query: string;
  cron?: string;
  interval?: number;
  statusBadge?: string;
  [key: string]: unknown;
}

export class MolaSearchError extends Error {
  constructor(
    readonly status: number,
    readonly body: string,
  ) {
    super(`MolaSearch API error ${status}: ${body}`);
    this.name = "MolaSearchError";
  }
}

export interface MolaSearchOptions {
  baseUrl?: string;
  webKey?: string;
  timeoutMs?: number;
}

export class MolaSearchClient {
  readonly baseUrl: string;
  private readonly webKey?: string;
  private readonly timeoutMs: number;

  constructor(opts: MolaSearchOptions = {}) {
    this.baseUrl = (opts.baseUrl || DEFAULT_BASE_URL).replace(/\/+$/, "");
    this.webKey = opts.webKey;
    this.timeoutMs = opts.timeoutMs ?? 120_000;
  }

  private headers(): Record<string, string> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json; charset=utf-8",
      Accept: "application/json",
    };
    if (this.webKey) {
      // `magpie web` names its sign-in cookie after the port it serves.
      const port = new URL(this.baseUrl).port || (this.baseUrl.startsWith("https:") ? "443" : "80");
      headers.Cookie = `magpie_web_${port}=${this.webKey}`;
    }
    return headers;
  }

  private async request<T>(path: string, method: "GET" | "POST" | "DELETE" = "GET", body?: unknown): Promise<T> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);
    try {
      const res = await fetch(`${this.baseUrl}${path}`, {
        method,
        headers: this.headers(),
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: controller.signal,
      });
      const text = await res.text();
      if (!res.ok) throw new MolaSearchError(res.status, text);
      return (text ? JSON.parse(text) : {}) as T;
    } finally {
      clearTimeout(timer);
    }
  }

  // -- Detective ----------------------------------------------------------

  /** The 30-mode catalog the engine serves. */
  async modes(): Promise<Array<Record<string, unknown>>> {
    const out = await this.request<{ modes: Array<Record<string, unknown>> }>("/api/detective/modes");
    return out.modes ?? [];
  }

  /** Run one live investigation. `ABSTAIN` with no citations is a valid answer. */
  investigate(req: InvestigateRequest): Promise<Investigation> {
    return this.request<Investigation>("/api/detective/investigate", "POST", {
      modeId: "M11",
      ...req,
    });
  }

  /** Provider health, the gates in force, and where the settings file lives. */
  state(): Promise<Record<string, unknown>> {
    return this.request("/api/detective/state");
  }

  /** Which search/vision keys are configured, and where each is read from. */
  keyStatus(): Promise<Record<string, unknown>> {
    return this.request("/api/detective/keys");
  }

  /** Fingerprint an image (dHash/aHash/EXIF) without running a search. */
  uploadImage(name: string, base64: string): Promise<Record<string, unknown>> {
    return this.request("/api/detective/upload-image", "POST", { name, base64 });
  }

  /** Turn one search provider on or off; the rest of a run keeps serving. */
  setChannel(id: string, enabled: boolean): Promise<Record<string, unknown>> {
    return this.request("/api/detective/channels", "POST", { id, enabled });
  }

  /** Write the gates the engine reads. The server validates and persists them. */
  saveSettings(settings: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.request("/api/detective/settings", "POST", settings);
  }

  // -- Stakeout -----------------------------------------------------------

  async stakeouts(): Promise<StakeoutJob[]> {
    const out = await this.request<{ jobs: StakeoutJob[] }>("/api/detective/stakeout");
    return out.jobs ?? [];
  }

  createStakeout(job: StakeoutJob): Promise<Record<string, unknown>> {
    return this.request("/api/detective/stakeout", "POST", job);
  }

  deleteStakeout(jobId: string): Promise<Record<string, unknown>> {
    return this.request(`/api/detective/stakeout/${encodeURIComponent(jobId)}`, "DELETE");
  }

  /** Re-check one watch, or every watch when `jobId` is omitted. */
  checkStakeout(jobId?: string): Promise<Record<string, unknown>> {
    return this.request("/api/detective/stakeout/check", "POST", jobId ? { jobId } : {});
  }

  /** Write (or remove) the skill and MCP entry in an agent's own folders. */
  injectAgent(agent: string, enabled = true): Promise<Record<string, unknown>> {
    return this.request("/api/detective/modes/inject", "POST", { agent, enabled });
  }

  // -- Memory store -------------------------------------------------------

  ragStatus(): Promise<Record<string, unknown>> {
    return this.request("/api/rag/status");
  }

  ragSearch(query: string, topK = 5): Promise<Record<string, unknown>> {
    return this.request("/api/rag/search", "POST", { query, topK });
  }

  /** Insert one CanonicalMemoryRecord; topic and evidenceText are required. */
  ragInsert(record: MemoryRecord): Promise<Record<string, unknown>> {
    return this.request("/api/rag/records", "POST", record);
  }

  /**
   * Three-phase hot swap: backup, dual-write, re-embed, hash verify, flip.
   * A verification mismatch throws and leaves the active backend unchanged.
   */
  ragHotswap(req: {
    targetDbId: string;
    embeddingModel?: string;
    rerankerModel?: string;
    memoryMode?: string;
  }): Promise<Record<string, unknown>> {
    return this.request("/api/rag/hotswap", "POST", req);
  }

  /** Write the corpus as USMF JSONL on the server; returns its path. */
  ragExport(): Promise<Record<string, unknown>> {
    return this.request("/api/rag/coldswap/export", "POST", {});
  }

  /** Import USMF JSONL from a string, or from a path on the server. */
  ragImport(req: { jsonl?: string; path?: string }): Promise<Record<string, unknown>> {
    return this.request("/api/rag/coldswap/import", "POST", req);
  }

  async habits(): Promise<Array<Record<string, unknown>>> {
    const out = await this.request<{ habits: Array<Record<string, unknown>> }>("/api/rag/habits");
    return out.habits ?? [];
  }
}
