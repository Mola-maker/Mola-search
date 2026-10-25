import type { SearchPage } from "../core/types.js";
import type { Aspect } from "./planner.js";
import { jaccard, tokenize, urlKey } from "../util/text.js";

/** 方面覆盖度：方面关键词在语料中的命中比例（关键词级）+ 文档级最大相似度，取两者较大值 */
export function aspectCoverage(aspect: Aspect, pages: SearchPage[]): number {
  return coverageFromCorpus(aspect, corpusForAspect(aspect, pages));
}

function corpusForAspect(aspect: Aspect, pages: SearchPage[]): Array<Set<string>> {
  const tagged = pages.filter((p) => p.aspects?.includes(aspect.id));
  const relevant = tagged.length > 0 ? tagged : pages.filter((p) => !p.aspects || p.aspects.length === 0);
  return relevant.map((p) => tokenize(`${p.title ?? ""} ${p.content ?? ""} ${p.passage ?? ""}`));
}

function coverageFromCorpus(aspect: Aspect, corpusTokens: Array<Set<string>>): number {
  if (aspect.keywords.length === 0) return 1;
  if (corpusTokens.length === 0) return 0;

  let hitKeywords = 0;
  for (const kw of aspect.keywords) {
    if (corpusTokens.some((t) => t.has(kw))) hitKeywords += 1;
  }
  const keywordCov = hitKeywords / aspect.keywords.length;

  const aspectTokens = tokenize(aspect.query);
  let bestDoc = 0;
  for (const t of corpusTokens) bestDoc = Math.max(bestDoc, jaccard(aspectTokens, t));
  // 关键词覆盖为主，文档相似度兜底（长查询关键词多易稀疏）
  return Math.max(keywordCov, bestDoc * 0.9);
}

export interface GapReport {
  /** 各方面覆盖率 0~1 */
  coverage: Array<{ aspectId: string; label: string; coverage: number }>;
  /** 总体覆盖率（平均） */
  overall: number;
  /** 未充分覆盖的方面 */
  weakAspects: Aspect[];
  /** 各盲区方面缺失的关键词（用于派生精化查询） */
  missingTerms: Map<string, string[]>;
  /** 从该方面实际返回结果中提取的新线索 */
  refinementTerms: Map<string, string[]>;
}

export function analyzeGaps(aspects: Aspect[], pages: SearchPage[], threshold: number): GapReport {
  const coverage: GapReport["coverage"] = [];
  const weakAspects: Aspect[] = [];
  const missingTerms = new Map<string, string[]>();
  const refinementTerms = new Map<string, string[]>();

  for (const aspect of aspects) {
    const corpusTokens = corpusForAspect(aspect, pages);
    const cov = coverageFromCorpus(aspect, corpusTokens);
    coverage.push({ aspectId: aspect.id, label: aspect.label, coverage: Math.round(cov * 1000) / 1000 });
    if (cov < threshold) {
      weakAspects.push(aspect);
      const missing = aspect.keywords.filter((kw) => !corpusTokens.some((t) => t.has(kw)));
      missingTerms.set(aspect.id, missing.slice(0, 5));
      refinementTerms.set(aspect.id, resultDrivenTerms(aspect, pages));
    }
  }
  const overall = coverage.length ? coverage.reduce((a, c) => a + c.coverage, 0) / coverage.length : 0;
  return { coverage, overall, weakAspects, missingTerms, refinementTerms };
}

function resultDrivenTerms(aspect: Aspect, pages: SearchPage[]): string[] {
  const tagged = pages.filter((page) => page.aspects?.includes(aspect.id));
  const relevant = tagged.length > 0 ? tagged : pages.filter((page) => !page.aspects || page.aspects.length === 0);
  const original = tokenize(aspect.query);
  const documentFrequency = new Map<string, number>();
  const generic = /^(?:一个|一些|进行|通过|相关|内容|信息|介绍|提供|包括|可以|我们|你们|他们|以及|已经|目前|最新|搜索|结果|报道|动态|标准|摘要|针对|关于|什么|如何|为什么|哪些|年的|今年|本次|年月|产品|发展|www|https?|com|cn|the|and|that|this|with|from|for|are|was|were|has|have|its|our|you|your|their|about|into|more|how|what|why|when|where|which|will|can|not|all|new|use|using|latest|search|results?|as|by|of|to|in|on|at|is|it|or)$/i;
  for (const page of relevant) {
    const terms = tokenize(`${page.title ?? ""} ${page.content ?? ""} ${page.passage ?? ""}`);
    for (const term of terms) {
      if (original.has(term) || generic.test(term) || /^\d+$/.test(term) || term.length < 2 || /^[的了和与及其在是有为于对]|[的和与]$/.test(term)) continue;
      documentFrequency.set(term, (documentFrequency.get(term) ?? 0) + 1);
    }
  }
  return [...documentFrequency.entries()]
    .sort((a, b) => b[1] - a[1] || b[0].length - a[0].length || a[0].localeCompare(b[0], "zh-CN"))
    .slice(0, 4)
    .map(([term]) => term);
}

/** 去重合并：URL 键优先，标题相似度 > 0.85 视为同文（保留得分最高者，并合并 aspects 轨迹） */
export function dedupeMerge(pages: SearchPage[]): SearchPage[] {
  const byUrl = new Map<string, SearchPage>();
  const kept: SearchPage[] = [];

  for (const p of pages) {
    const key = p.url?.trim() ? `u:${pageUrlKey(p.url)}` : null;
    const existing = key ? byUrl.get(key) : undefined;
    if (existing) {
      mergeInto(existing, p);
      continue;
    }
    const twin = kept.find((k) => titleSim(k.title, p.title) > 0.85 && !hasDistinctIds(k.url, p.url));
    if (twin) {
      mergeInto(twin, p);
      if (key) byUrl.set(key, twin);
      continue;
    }
    if (key) byUrl.set(key, p);
    kept.push(p);
  }
  return kept;
}

function pageUrlKey(url: string): string {
  try {
    const u = new URL(url);
    const params = [...u.searchParams.entries()]
      .filter(([name]) => !/^(?:utm_.+|spm|fbclid|gclid)$/i.test(name))
      .sort(([aName, aValue], [bName, bValue]) => aName.localeCompare(bName) || aValue.localeCompare(bValue));
    const query = params.map(([name, value]) => `${encodeURIComponent(name)}=${encodeURIComponent(value)}`).join("&");
    return `${urlKey(url)}${query ? `?${query}` : ""}`;
  } catch {
    return urlKey(url);
  }
}

function hasDistinctIds(a?: string, b?: string): boolean {
  const aIds = urlIds(a);
  const bIds = urlIds(b);
  return aIds.length > 0 && bIds.length > 0 && aIds.join("&") !== bIds.join("&");
}

function urlIds(url?: string): string[] {
  if (!url) return [];
  try {
    return [...new URL(url).searchParams.entries()]
      .filter(([name]) => /id$/i.test(name))
      .map(([name, value]) => `${name.toLowerCase()}=${value}`)
      .sort();
  } catch {
    return [];
  }
}

function titleSim(a?: string, b?: string): number {
  if (!a || !b) return 0;
  return jaccard(tokenize(a), tokenize(b));
}

function mergeInto(target: SearchPage, src: SearchPage): void {
  target.aspects = [...new Set([...(target.aspects ?? []), ...(src.aspects ?? [])])];
  target.round = Math.min(target.round ?? 99, src.round ?? 99);
  if ((src.score ?? 0) > (target.score ?? 0)) {
    target.score = src.score;
    if (src.content) target.content = src.content;
  }
  if (!target.passage && src.passage) target.passage = src.passage;
  if (!target.authority_level && src.authority_level) target.authority_level = src.authority_level;
}
