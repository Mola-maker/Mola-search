import { planAspects, refineAspect, variantsFor } from "./planner.js";
import { analyzeGaps, dedupeMerge } from "./analyst.js";
import { synthesize } from "./synthesizer.js";
import { newId } from "../util/text.js";
/**
 * 群智能体迭代搜索引擎：
 *   Planner（分解） → Searcher Swarm（并行多路检索） → Analyst（覆盖度/盲区） → 精化下一轮 → … → Synthesizer（汇总成报告）
 * 收敛条件：总体覆盖率 ≥ profile.coverageThreshold，或无新增精化查询，或达到 maxRounds。
 */
export class SwarmEngine {
    client;
    history;
    constructor(client, history) {
        this.client = client;
        this.history = history;
    }
    async run(opts) {
        const { query, profile } = opts;
        const overrides = opts.overrides ?? {};
        const started = Date.now();
        const seenQueries = new Set();
        const originalAspects = planAspects(query, profile);
        let aspects = originalAspects;
        const aspectRoots = new Map(originalAspects.map((aspect) => [aspect.id, aspect.id]));
        for (const a of originalAspects)
            seenQueries.add(a.query);
        const corpus = [];
        const trace = [];
        const failures = [];
        let totalCalls = 0;
        let successfulCalls = 0;
        let cacheHits = 0;
        let rawHits = 0;
        let uniqueHits = 0;
        let gap = null;
        let converged = false;
        let reason = "达到最大轮次";
        let round = 0;
        for (; round < profile.maxRounds; round++) {
            await opts.onProgress?.({
                round,
                message: `第 ${round + 1} 轮：${aspects.length} 个方面 × ${profile.searchersPerQuery} 个搜索器并行检索`,
                ratio: round / Math.max(1, profile.maxRounds),
            });
            this.throwIfAborted(opts.signal);
            // —— Searcher Swarm：所有方面 × 变体并行（每查询带 aspect/round 标记）——
            const tasks = [];
            let rawHitsThisRound = 0;
            for (const aspect of aspects) {
                for (const variant of variantsFor(aspect, profile, round, overrides)) {
                    totalCalls += 1;
                    tasks.push(this.client
                        .search(variant, opts.signal)
                        .then(({ response, stats }) => {
                        successfulCalls += 1;
                        if (stats.cacheHit)
                            cacheHits += 1;
                        rawHits += response.pages.length;
                        rawHitsThisRound += response.pages.length;
                        for (const page of response.pages) {
                            corpus.push({ ...page, aspects: [aspectRoots.get(aspect.id) ?? aspect.id], round });
                        }
                    })
                        .catch((err) => {
                        failures.push(`${aspect.query}: ${err.code ?? "UnknownError"}: ${err.message}`);
                    }));
                }
            }
            await Promise.allSettled(tasks);
            this.throwIfAborted(opts.signal);
            if (successfulCalls === 0) {
                const detail = failures.length ? `：${failures.slice(0, 3).join("；")}` : "";
                throw new Error(`全部 ${totalCalls} 次搜索请求均失败${detail}`);
            }
            // —— Analyst：去重合并 + 覆盖率 + 盲区 ——
            const merged = dedupeMerge(corpus);
            gap = analyzeGaps(originalAspects, merged, profile.coverageThreshold);
            const newUnique = Math.max(0, merged.length - uniqueHits);
            uniqueHits = merged.length;
            trace.push({
                round,
                queries: aspects.map((a) => a.query),
                hits: rawHitsThisRound,
                newUnique,
                coverageAfter: Math.round(gap.overall * 1000) / 1000,
            });
            if (gap.overall >= profile.coverageThreshold) {
                converged = true;
                reason = `覆盖率 ${gap.overall.toFixed(3)} ≥ 阈值 ${profile.coverageThreshold}`;
                break;
            }
            // —— 精化：盲区方面派生下一轮子查询（跳过已用过的查询，防循环）——
            const refined = [];
            for (const weak of gap.weakAspects) {
                const clues = [
                    ...(gap.refinementTerms.get(weak.id) ?? []),
                    ...(gap.missingTerms.get(weak.id) ?? []),
                ];
                const r = refineAspect(weak, clues, round + 1);
                if (r && !seenQueries.has(r.query) && r.query !== weak.query) {
                    seenQueries.add(r.query);
                    aspectRoots.set(r.id, weak.id);
                    refined.push(r);
                }
                else if (r) {
                    // 措辞角度轮转后仍重复 → 直接以盲区关键词重组
                    const alt = refineAspect(weak, clues, round + 2);
                    if (alt && !seenQueries.has(alt.query)) {
                        seenQueries.add(alt.query);
                        aspectRoots.set(alt.id, weak.id);
                        refined.push(alt);
                    }
                }
            }
            if (refined.length === 0) {
                reason = "无可用精化查询（盲区无法进一步补齐）";
                break;
            }
            aspects = refined;
        }
        await opts.onProgress?.({ round, message: "汇总报告生成中", ratio: 0.95 });
        const mergedFinal = dedupeMerge(corpus);
        const finalGap = gap ?? analyzeGaps(originalAspects, mergedFinal, profile.coverageThreshold);
        const report = synthesize(query, profile.name, profile.strategy, mergedFinal, finalGap, trace, { totalApiCalls: totalCalls, rawHits, cacheHits, elapsedMs: Date.now() - started }, converged, reason);
        let historyId = null;
        let warning;
        const candidateId = newId("swarm");
        try {
            this.history.save({
                id: candidateId,
                createdAt: new Date().toISOString(),
                query,
                profile: profile.name,
                rounds: report.roundsExecuted,
                totalCalls,
                totalHits: rawHits,
                uniqueHits: report.stats.uniqueHits,
                coverage: report.overallCoverage,
                report,
            });
            if (!this.history.get(candidateId))
                throw new Error("保存后无法读取历史记录");
            historyId = candidateId;
        }
        catch (err) {
            warning = `历史保存失败，本次报告未持久化：${err instanceof Error ? err.message : String(err)}`;
            report.warning = warning;
        }
        await opts.onProgress?.({ round, message: warning ?? "完成", ratio: 1 });
        return { report, historyId, ...(warning ? { warning } : {}) };
    }
    throwIfAborted(signal) {
        if (signal?.aborted)
            throw new Error("已取消（aborted）");
    }
}
