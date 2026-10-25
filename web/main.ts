type UnknownRecord = Record<string, unknown>;
type SearchKind = "single" | "deep";
type SceneState = "idle" | "planning" | "searching" | "analyzing" | "synthesizing" | "complete" | "error" | "cancelled";

const STAGES: SceneState[] = ["planning", "searching", "analyzing", "synthesizing", "complete"];
const STAGE_LABELS: Record<SceneState, string> = {
  idle: "准备就绪",
  planning: "正在规划问题",
  searching: "Agent 正在并行检索",
  analyzing: "正在分析信息盲区",
  synthesizing: "正在汇总研究发现",
  complete: "探索完成",
  error: "搜索遇到问题",
  cancelled: "已停止本次搜索",
};

function byId<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) throw new Error(`页面缺少必要元素：${id}`);
  return element as T;
}

function isRecord(value: unknown): value is UnknownRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringValue(value: unknown, fallback = ""): string {
  return typeof value === "string" && value.trim() ? value.trim() : fallback;
}

function numberValue(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function arrayValue(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function element<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
  text?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function safeUrl(value: unknown): string | null {
  if (typeof value !== "string" || !value.trim()) return null;
  try {
    const url = new URL(value);
    return url.protocol === "https:" || url.protocol === "http:" ? url.href : null;
  } catch {
    return null;
  }
}

function hostname(value: string | null): string {
  if (!value) return "";
  try {
    return new URL(value).hostname.replace(/^www\./, "");
  } catch {
    return "";
  }
}

const form = byId<HTMLFormElement>("search-form");
const input = byId<HTMLInputElement>("search-input");
const queryCount = byId("query-count");
const searchButton = byId<HTMLButtonElement>("search-button");
const cancelButton = byId<HTMLButtonElement>("cancel-button");
const formMessage = byId("form-message");
const progressSection = byId("progress-section");
const progressTitle = byId("progress-title");
const progressMessage = byId("progress-message");
const stageItems = Array.from(document.querySelectorAll<HTMLLIElement>("#stage-list li[data-stage]"));
const resultsSection = byId("results-section");
const resultSummary = byId("result-summary");
const resultsList = byId("results-list");
const resultCount = byId("source-count");
const emptyResults = byId("empty-results");
const reportPanel = byId("report-panel");
const overallCoverage = byId("overall-coverage");
const reportStats = byId("report-stats");
const coveragePanel = byId("coverage-panel");
const coverageList = byId("coverage-list");
const keyFactsPanel = byId("key-facts-panel");
const keyFactsList = byId("key-facts-list");
const openGapsPanel = byId("open-gaps-panel");
const openGapsList = byId("open-gaps-list");
const announcer = byId("announcer");
const sceneContainer = byId("yuanbao-scene");
const sceneFallback = byId("scene-fallback");
const sceneStateLabel = byId("scene-state-label");
const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");

let activeController: AbortController | null = null;
let requestTimer: ReturnType<typeof setTimeout> | undefined;
let searching = false;
let yuanbaoScene: { setStage: (stage: string) => void } | null = null;

function announce(message: string): void {
  announcer.textContent = "";
  window.setTimeout(() => {
    announcer.textContent = message;
  }, 30);
}

function setBusy(busy: boolean): void {
  searching = busy;
  searchButton.disabled = busy;
  searchButton.setAttribute("aria-disabled", String(busy));
  cancelButton.hidden = !busy;
  input.setAttribute("aria-busy", String(busy));
}

function setStage(stage: SceneState): void {
  sceneStateLabel.textContent = STAGE_LABELS[stage];
  yuanbaoScene?.setStage(stage);
  const activeIndex = STAGES.indexOf(stage);
  for (const item of stageItems) {
    const itemStage = item.dataset.stage as SceneState;
    const itemIndex = STAGES.indexOf(itemStage);
    item.classList.toggle("is-active", itemStage === stage);
    item.classList.toggle("is-complete", activeIndex >= 0 && itemIndex < activeIndex);
    if (itemStage === stage) item.setAttribute("aria-current", "step");
    else item.removeAttribute("aria-current");
  }
}

function selectedKind(): SearchKind {
  return form.querySelector<HTMLInputElement>('input[name="search-kind"]:checked')?.value === "deep"
    ? "deep"
    : "single";
}

function requestPayload(query: string): UnknownRecord {
  const mode = Number(byId<HTMLSelectElement>("search-mode").value);
  const industry = byId<HTMLSelectElement>("search-industry").value;
  const freshness = byId<HTMLSelectElement>("search-freshness").value;
  return {
    query,
    kind: selectedKind() === "deep" ? "swarm" : "single",
    mode,
    ...(industry ? { industry } : {}),
    ...(freshness ? { freshness } : {}),
  };
}

function resetOutput(): void {
  resultsSection.hidden = true;
  reportPanel.hidden = true;
  resultsList.replaceChildren();
  reportStats.replaceChildren();
  coverageList.replaceChildren();
  keyFactsList.replaceChildren();
  openGapsList.replaceChildren();
  emptyResults.hidden = true;
  formMessage.textContent = "";
  input.removeAttribute("aria-invalid");
}

function formatPercent(value: unknown): string {
  const number = numberValue(value);
  if (number === null) return "—";
  const normalized = number > 1 ? Math.min(number, 100) : Math.max(0, number) * 100;
  return `${Math.round(normalized)}%`;
}

function formatDuration(value: unknown): string {
  const milliseconds = numberValue(value);
  if (milliseconds === null) return "—";
  return milliseconds >= 1000 ? `${(milliseconds / 1000).toFixed(1)} s` : `${Math.round(milliseconds)} ms`;
}

function renderReport(report: UnknownRecord): void {
  reportPanel.hidden = false;
  overallCoverage.textContent = formatPercent(report.overallCoverage);

  const stats = isRecord(report.stats) ? report.stats : {};
  const entries: Array<[string, string]> = [
    ["研究轮次", numberValue(report.roundsExecuted)?.toString() ?? "—"],
    ["搜索请求", numberValue(stats.totalApiCalls)?.toString() ?? "—"],
    ["原始命中", numberValue(stats.rawHits)?.toString() ?? "—"],
    ["去重来源", numberValue(stats.uniqueHits)?.toString() ?? "—"],
    ["研究耗时", formatDuration(stats.elapsedMs)],
  ];
  const statsNodes = entries.map(([label, value]) => {
    const wrapper = element("div");
    wrapper.append(element("dt", undefined, label), element("dd", undefined, value));
    return wrapper;
  });
  reportStats.replaceChildren(...statsNodes);

  const coverageNodes = arrayValue(report.coverage).flatMap((item) => {
    if (!isRecord(item)) return [];
    const row = element("div", "coverage-item");
    const header = element("div", "coverage-item-header");
    header.append(
      element("span", undefined, stringValue(item.label, "研究主题")),
      element("span", undefined, formatPercent(item.coverage)),
    );
    const track = element("div", "coverage-track");
    const bar = element("span", "coverage-bar");
    const raw = numberValue(item.coverage) ?? 0;
    const percent = raw > 1 ? Math.min(raw, 100) : Math.max(0, raw) * 100;
    bar.style.width = `${percent}%`;
    track.setAttribute("role", "progressbar");
    track.setAttribute("aria-label", `${stringValue(item.label, "研究主题")}覆盖率`);
    track.setAttribute("aria-valuemin", "0");
    track.setAttribute("aria-valuemax", "100");
    track.setAttribute("aria-valuenow", String(Math.round(percent)));
    track.append(bar);
    row.append(header, track);
    return [row];
  });
  coverageList.replaceChildren(...coverageNodes);
  coveragePanel.hidden = coverageNodes.length === 0;

  const factNodes = arrayValue(report.keyFacts).flatMap((fact) => {
    const text = stringValue(fact);
    return text ? [element("li", undefined, text)] : [];
  });
  keyFactsList.replaceChildren(...factNodes);
  keyFactsPanel.hidden = factNodes.length === 0;

  const gapNodes = arrayValue(report.openGaps).flatMap((gap) => {
    const text = stringValue(gap);
    return text ? [element("li", undefined, text)] : [];
  });
  openGapsList.replaceChildren(...gapNodes);
  openGapsPanel.hidden = gapNodes.length === 0;
}

function renderPage(page: UnknownRecord, index: number): HTMLElement {
  const card = element("article", "result-card");
  const top = element("div", "result-card-top");
  const url = safeUrl(page.url);
  const site = stringValue(page.site, hostname(url) || "网页来源");
  top.append(
    element("span", "result-number", String(index + 1).padStart(2, "0")),
    element("span", "result-site", site),
  );
  const date = stringValue(page.date);
  if (date) top.append(element("time", "result-date", date.slice(0, 16)));

  const heading = element("h4");
  const title = stringValue(page.title, "未命名结果");
  if (url) {
    const link = element("a", undefined, title);
    link.href = url;
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    link.setAttribute("aria-label", `${title}（在新窗口打开）`);
    heading.append(link);
  } else {
    heading.textContent = title;
  }

  const snippet = stringValue(page.content, stringValue(page.passage, "来源未提供摘要，可打开原文查看详细内容。"));
  const footer = element("div", "result-card-footer");
  const authority = numberValue(page.authority_level);
  const round = numberValue(page.round);
  if (authority !== null) footer.append(element("span", "result-tag", `权威 ${authority}/5`));
  if (round !== null) footer.append(element("span", "result-tag", `第 ${round + 1} 轮`));
  const aspects = arrayValue(page.aspects).flatMap((aspect) => {
    const text = stringValue(aspect);
    return text ? [element("span", "result-tag", text)] : [];
  });
  footer.append(...aspects.slice(0, 2));
  if (url) footer.append(element("span", "result-link-label", "查看原文 ↗"));
  card.append(top, heading, element("p", "result-snippet", snippet), footer);
  return card;
}

function extractResult(payload: UnknownRecord): { pages: UnknownRecord[]; report: UnknownRecord | null; stats: UnknownRecord } {
  const response = isRecord(payload.response) ? payload.response : null;
  const report = isRecord(payload.report) ? payload.report : null;
  const rawPages = Array.isArray(payload.pages)
    ? payload.pages
    : response && Array.isArray(response.pages)
      ? response.pages
      : report && Array.isArray(report.citations)
        ? report.citations
        : [];
  const pages = rawPages.filter(isRecord);
  const directStats = isRecord(payload.stats) ? payload.stats : {};
  return { pages, report, stats: directStats };
}

function renderResult(payload: UnknownRecord, kind: SearchKind): void {
  const { pages, report, stats } = extractResult(payload);
  resultsList.replaceChildren(...pages.map((page, index) => renderPage(page, index)));
  resultCount.textContent = `${pages.length} 个来源`;
  emptyResults.hidden = pages.length > 0;

  if (kind === "deep" && report) {
    renderReport(report);
    const rounds = numberValue(report.roundsExecuted);
    resultSummary.textContent = `${pages.length} 个去重来源${rounds === null ? "" : ` · ${rounds} 轮迭代研究`}`;
  } else {
    reportPanel.hidden = true;
    const latency = numberValue(stats.latencyMs);
    const cacheHit = stats.cacheHit === true;
    resultSummary.textContent = `${pages.length} 个相关结果${latency === null ? "" : ` · ${formatDuration(latency)}`}${cacheHit ? " · 缓存命中" : ""}`;
  }

  resultsSection.hidden = false;
  progressTitle.textContent = "探索完成";
  progressMessage.textContent = pages.length ? `已整理 ${pages.length} 个网页来源。` : "本次搜索已完成。";
  setStage("complete");
  announce(`搜索完成，共 ${pages.length} 个来源`);
  resultsSection.scrollIntoView({ behavior: reducedMotion.matches ? "auto" : "smooth", block: "start" });
}

function normalizeStage(event: UnknownRecord): { stage: SceneState; message: string; round: number | null } {
  const nested = isRecord(event.stage) ? event.stage : null;
  const rawStage = stringValue(nested ? nested.stage : event.stage, "searching").toLowerCase();
  const stage: SceneState = STAGES.includes(rawStage as SceneState)
    ? (rawStage as SceneState)
    : rawStage.includes("plan")
      ? "planning"
      : rawStage.includes("analy") || rawStage.includes("gap")
        ? "analyzing"
        : rawStage.includes("synth") || rawStage.includes("report")
          ? "synthesizing"
          : "searching";
  const message = stringValue(event.message, nested ? stringValue(nested.message) : "");
  const round = numberValue(event.round) ?? (nested ? numberValue(nested.round) : null);
  return { stage, message, round };
}

function handleStage(event: UnknownRecord): void {
  const { stage, message, round } = normalizeStage(event);
  setStage(stage);
  const roundLabel = round === null ? "" : `第 ${round} 轮 · `;
  progressMessage.textContent = `${roundLabel}${message || STAGE_LABELS[stage]}`;
}

function eventError(event: UnknownRecord): string {
  if (typeof event.error === "string") return event.error;
  if (isRecord(event.error)) return stringValue(event.error.message, "搜索服务返回了错误，请稍后重试。");
  return stringValue(event.message, "搜索服务返回了错误，请稍后重试。");
}

async function responseError(response: Response): Promise<string> {
  const fallback = `搜索服务请求失败（HTTP ${response.status}），请稍后重试。`;
  try {
    const text = await response.text();
    const firstLine = text.split(/\r?\n/, 1)[0];
    const data: unknown = JSON.parse(firstLine);
    if (isRecord(data)) return eventError(data) || fallback;
  } catch {
    // A non-JSON HTTP error uses the readable status fallback below.
  }
  return fallback;
}

async function readEvents(response: Response, kind: SearchKind): Promise<void> {
  if (!response.body) throw new Error("浏览器无法读取搜索流，请使用支持流式响应的现代浏览器。");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let receivedResult = false;

  const processLine = (line: string): void => {
    if (!line.trim()) return;
    const parsed: unknown = JSON.parse(line);
    if (!isRecord(parsed)) throw new Error("搜索服务返回了无法识别的数据。");
    if (parsed.type === "error" || "error" in parsed) throw new Error(eventError(parsed));
    if (parsed.type === "result" || "result" in parsed) {
      const result = isRecord(parsed.result) ? parsed.result : parsed;
      renderResult(result, kind);
      receivedResult = true;
      return;
    }
    if (parsed.type === "stage" || "stage" in parsed) handleStage(parsed);
  };

  while (true) {
    const { done, value } = await reader.read();
    buffer += decoder.decode(value, { stream: !done });
    const lines = buffer.split(/\r?\n/);
    buffer = done ? "" : (lines.pop() ?? "");
    for (const line of lines) processLine(line);
    if (done) {
      if (buffer.trim()) processLine(buffer);
      break;
    }
  }
  if (!receivedResult) throw new Error("搜索流已结束，但没有收到结果。请重试。");
}

function readableError(error: unknown): string {
  if (error instanceof TypeError) return "无法连接本地搜索服务，请确认服务已启动后重试。";
  if (error instanceof SyntaxError) return "搜索服务返回了无法识别的数据，请稍后重试。";
  if (error instanceof Error && error.message) return error.message.slice(0, 300);
  return "搜索未完成，请稍后重试。";
}

async function runSearch(query: string): Promise<void> {
  if (searching) return;
  const kind = selectedKind();
  const controller = new AbortController();
  activeController = controller;
  let timedOut = false;
  setBusy(true);
  resetOutput();
  progressSection.hidden = false;
  progressTitle.textContent = kind === "deep" ? "正在深度探索" : "正在探索";
  progressMessage.textContent = "正在连接搜索服务…";
  setStage("planning");
  announce(kind === "deep" ? "深度搜索已开始" : "搜索已开始");

  requestTimer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, kind === "deep" ? 300_000 : 120_000);

  try {
    const response = await fetch("/api/search", {
      method: "POST",
      headers: {
        Accept: "application/x-ndjson",
        "Content-Type": "application/json",
      },
      body: JSON.stringify(requestPayload(query)),
      signal: controller.signal,
    });
    if (!response.ok) throw new Error(await responseError(response));
    await readEvents(response, kind);
  } catch (error) {
    if (controller.signal.aborted) {
      if (timedOut) {
        const message = "搜索等待超时，请稍后重试或改为单次搜索。";
        formMessage.textContent = message;
        progressTitle.textContent = "搜索已超时";
        progressMessage.textContent = message;
        setStage("error");
        announce(message);
      } else {
        progressTitle.textContent = "搜索已取消";
        progressMessage.textContent = "已停止本次搜索，可以修改问题后重新探索。";
        setStage("cancelled");
        announce("搜索已取消");
      }
    } else {
      const message = readableError(error);
      formMessage.textContent = message;
      progressTitle.textContent = "搜索未完成";
      progressMessage.textContent = "本次探索中断，请根据提示重试。";
      setStage("error");
      announce(`搜索失败：${message}`);
    }
  } finally {
    if (requestTimer !== undefined) clearTimeout(requestTimer);
    requestTimer = undefined;
    if (activeController === controller) {
      activeController = null;
      setBusy(false);
    }
  }
}

form.addEventListener("submit", (event) => {
  event.preventDefault();
  if (searching) return;
  const query = input.value.trim();
  if (!query) {
    formMessage.textContent = "请先输入一个想探索的问题。";
    input.setAttribute("aria-invalid", "true");
    input.focus();
    return;
  }
  if (query.length > 100) {
    formMessage.textContent = "问题最多 100 字，请精简后再试。";
    input.setAttribute("aria-invalid", "true");
    input.focus();
    return;
  }
  void runSearch(query);
});

cancelButton.addEventListener("click", () => {
  activeController?.abort();
});

input.addEventListener("input", () => {
  queryCount.textContent = `${input.value.length} / 100`;
  formMessage.textContent = "";
  input.removeAttribute("aria-invalid");
});

for (const button of document.querySelectorAll<HTMLButtonElement>("button[data-query]")) {
  button.addEventListener("click", () => {
    input.value = button.dataset.query ?? "";
    input.dispatchEvent(new Event("input"));
    input.focus();
  });
}

for (const radio of form.querySelectorAll<HTMLInputElement>('input[name="search-kind"]')) {
  radio.addEventListener("change", () => {
    const label = searchButton.querySelector("span");
    if (label) label.textContent = selectedKind() === "deep" ? "开始深搜" : "开始探索";
  });
}

const logo = byId<HTMLImageElement>("brand-logo");
const showLogoFallback = (): void => logo.parentElement?.classList.add("logo-failed");
logo.addEventListener("error", showLogoFallback);
if (logo.complete && logo.naturalWidth === 0) showLogoFallback();

async function initializeScene(): Promise<void> {
  try {
    const sceneModule = (await import("./scene")) as {
      createYuanbaoScene: (container: HTMLElement) => { setStage: (stage: string) => void };
    };
    yuanbaoScene = await sceneModule.createYuanbaoScene(sceneContainer);
    yuanbaoScene.setStage("idle");
    for (const canvas of sceneContainer.querySelectorAll("canvas")) {
      canvas.setAttribute("role", "img");
      canvas.setAttribute("aria-label", "元宝 Agent 三维协作动画；搜索状态与结果同时以文字展示");
    }
    sceneContainer.classList.add("scene-ready");
  } catch {
    sceneContainer.classList.add("scene-unavailable");
    const message = sceneFallback.querySelector("p");
    if (message) message.textContent = "当前设备未启用 3D，搜索功能不受影响。";
  }
}

void initializeScene();
