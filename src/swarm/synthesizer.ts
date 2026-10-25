import type { SearchPage } from "../core/types.js";
import type { GapReport } from "./analyst.js";
import { clamp } from "../util/text.js";

export interface Finding {
  aspectId: string;
  aspectLabel: string;
  topPages: Array<{
    title: string;
    url: string;
    site: string;
    date: string;
    authority: number | null;
    score: number;
    snippet: string;
  }>;
}

export interface SynthReport {
  query: string;
  profile: string;
  strategy: string;
  roundsExecuted: number;
  converged: boolean;
  convergenceReason: string;
  stats: {
    totalApiCalls: number;
    rawHits: number;
    uniqueHits: number;
    cacheHits: number;
    elapsedMs: number;
  };
  iterationTrace: Array<{
    round: number;
    queries: string[];
    hits: number;
    newUnique: number;
    coverageAfter: number;
  }>;
  coverage: GapReport["coverage"];
  overallCoverage: number;
  findings: Finding[];
  keyFacts: string[];
  openGaps: string[];
  /** 便于模型直接引用的引用列表 */
  citations: Array<{ n: number; url: string; title: string; site: string }>;
  warning?: string;
}

export function synthesize(
  query: string,
  profileName: string,
  strategy: string,
  pages: SearchPage[],
  gap: GapReport,
  trace: SynthReport["iterationTrace"],
  stats: { totalApiCalls: number; rawHits: number; cacheHits: number; elapsedMs: number },
  converged: boolean,
  reason: string,
): SynthReport {
  const ranked = rankPages(pages);

  // 按方面聚类（一个页面可命中多个方面）
  const findings: Finding[] = [];
  const seen = new Set<string>();
  for (const aspectCover of gap.coverage) {
    const forAspect = ranked.filter((p) => (p.aspects ?? []).includes(aspectCover.aspectId)).slice(0, 5);
    if (forAspect.length === 0) continue;
    findings.push({
      aspectId: aspectCover.aspectId,
      aspectLabel: aspectCover.label,
      topPages: forAspect.map((p) => ({
        title: p.title ?? "(无标题)",
        url: p.url ?? "",
        site: p.site ?? "",
        date: p.date ?? "",
        authority: p.authority_level ?? null,
        score: p.score ?? 0,
        snippet: (p.content || p.passage || "").slice(0, 220),
      })),
    });
    for (const p of forAspect) if (p.url) seen.add(p.url);
  }
  // 未被任何方面收编的高分页兜底进 findings
  const rest = ranked.filter((p) => p.url && !seen.has(p.url)).slice(0, 5);
  if (rest.length > 0) {
    findings.push({
      aspectId: "other",
      aspectLabel: "其他高相关",
      topPages: rest.map((p) => ({
        title: p.title ?? "(无标题)",
        url: p.url ?? "",
        site: p.site ?? "",
        date: p.date ?? "",
        authority: p.authority_level ?? null,
        score: p.score ?? 0,
        snippet: (p.content || p.passage || "").slice(0, 220),
      })),
    });
  }

  const citations = ranked.slice(0, 20).map((p, i) => ({
    n: i + 1,
    url: p.url ?? "",
    title: p.title ?? "(无标题)",
    site: p.site ?? "",
  }));

  const keyFacts = ranked
    .slice(0, 8)
    .map((p) => (p.content || p.passage || "").trim())
    .filter((s) => s.length > 20)
    .map((s) => s.slice(0, 300));

  const openGaps = gap.weakAspects.map((a) => {
    const missing = gap.missingTerms.get(a.id) ?? [];
    return `${a.label}（覆盖率不足）：建议补充检索 ${missing.length ? `「${missing.slice(0, 3).join(" ")}」` : a.query}`;
  });

  return {
    query,
    profile: profileName,
    strategy,
    roundsExecuted: trace.length,
    converged,
    convergenceReason: reason,
    stats: {
      totalApiCalls: stats.totalApiCalls,
      rawHits: stats.rawHits,
      uniqueHits: ranked.length,
      cacheHits: stats.cacheHits,
      elapsedMs: stats.elapsedMs,
    },
    iterationTrace: trace,
    coverage: gap.coverage,
    overallCoverage: Math.round(gap.overall * 1000) / 1000,
    findings,
    keyFacts,
    openGaps,
    citations,
  };
}

/** 综合排序：0.6*相关性 + 0.25*权威等级 + 0.15*新鲜度（低轮次补充的略降权） */
export function rankPages(pages: SearchPage[]): SearchPage[] {
  const now = Date.now();
  return [...pages].sort((a, b) => pageWeight(b, now) - pageWeight(a, now));
}

function pageWeight(p: SearchPage, now: number): number {
  const rel = clamp(p.score ?? 0, 0, 1);
  const auth = clamp((p.authority_level ?? 2) / 5, 0, 1);
  const days = p.date ? Math.max(0, (now - Date.parse(p.date)) / 86400000) : 365;
  const fresh = clamp(1 - days / 365, 0, 1);
  const roundPenalty = p.round !== undefined && p.round > 0 ? 0.95 ** p.round : 1;
  return (0.6 * rel + 0.25 * auth + 0.15 * fresh) * roundPenalty;
}
