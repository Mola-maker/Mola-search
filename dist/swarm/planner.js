import { tokenize } from "../util/text.js";
/** 搜索器变体：同一子查询的不同参数组合，模拟「不同智能体各查一路」 */
export function variantsFor(aspect, profile, round, overrides) {
    const variants = [];
    const seen = new Set();
    const n = Math.max(1, Math.min(3, profile.searchersPerQuery));
    const modes = [0, 2, 1];
    for (let i = 0; i < n; i++) {
        const mode = n === 1 ? (overrides.mode ?? 0) : (overrides.mode ?? modes[i]);
        const variant = {
            query: aspect.query,
            mode,
            cnt: overrides.cnt ?? profile.cnt,
        };
        const freshness = overrides.freshness ?? profile.freshness;
        if (freshness)
            variant.freshness = round === 0 ? freshness : undefined; // 后续轮次放开时效以补盲区
        const industry = overrides.industry ?? profile.industry;
        if (industry && i % 2 === 0)
            variant.industry = industry; // 部分搜索器走行业定向
        if (overrides.site)
            variant.site = overrides.site;
        const key = JSON.stringify([variant.query, variant.mode, variant.cnt, variant.freshness, variant.industry, variant.site]);
        if (!seen.has(key)) {
            seen.add(key);
            variants.push(variant);
        }
    }
    return variants;
}
/** 策略模板：不同 strategy 产生不同的方面分解 */
const STRATEGY_TEMPLATES = {
    comprehensive: [
        { label: "核心定义", suffix: "" },
        { label: "最新进展", suffix: " 最新进展 2026" },
        { label: "原理机制", suffix: " 原理 机制" },
        { label: "应用实践", suffix: " 应用 案例 实践" },
        { label: "风险挑战", suffix: " 问题 挑战 争议" },
    ],
    news: [
        { label: "最新动态", suffix: "" },
        { label: "热点事件", suffix: " 热点 事件" },
        { label: "各方回应", suffix: " 回应 评论" },
    ],
    academic: [
        { label: "综述脉络", suffix: " 综述" },
        { label: "方法模型", suffix: " 方法 模型" },
        { label: "实验数据", suffix: " 实验 数据集" },
        { label: "开放问题", suffix: " 局限 未来方向" },
    ],
    finance: [
        { label: "行情动态", suffix: "" },
        { label: "公司公告", suffix: " 公告 财报" },
        { label: "机构观点", suffix: " 研报 评级" },
        { label: "政策监管", suffix: " 政策 监管" },
    ],
    quick: [{ label: "直接命中", suffix: "" }],
};
/** 分解规则：显式分隔符优先；否则按策略模板展开；方面数受 profile.maxSubqueries 约束 */
export function planAspects(query, profile) {
    const parts = splitExplicit(query);
    const maxN = Math.max(1, profile.maxSubqueries);
    const aspects = [];
    if (parts.length > 1) {
        // 用户给了多主题复合查询 → 每个主题一个方面，模板后缀轮转补充深度
        const template = STRATEGY_TEMPLATES[profile.strategy] ?? STRATEGY_TEMPLATES.comprehensive;
        for (let i = 0; i < Math.min(parts.length, maxN); i++) {
            const base = parts[i].trim();
            const suffix = i === 0 ? "" : template[i % template.length].suffix;
            const q = composeQuery(base, suffix && profile.strategy !== "quick" ? suffix : "");
            aspects.push(makeAspect(`a${i}`, i === 0 ? "主主题" : `子主题${i}`, q));
        }
    }
    else {
        const template = STRATEGY_TEMPLATES[profile.strategy] ?? STRATEGY_TEMPLATES.comprehensive;
        for (let i = 0; i < Math.min(template.length, maxN); i++) {
            const t = template[i];
            const q = composeQuery(query, t.suffix);
            aspects.push(makeAspect(`a${i}`, t.label, q));
        }
    }
    return aspects.slice(0, maxN);
}
function composeQuery(base, suffix) {
    const existing = base.toLowerCase();
    const additions = suffix
        .trim()
        .split(/\s+/)
        .filter((part) => part && !existing.includes(part.toLowerCase()));
    return [base.trim(), ...additions].filter(Boolean).join(" ");
}
/** 由 Analyst 在迭代中调用：根据盲区方面派生新一轮的精化子查询 */
export function refineAspect(aspect, uncoveredTerms, round) {
    const existingTokens = tokenize(aspect.query);
    const seen = new Set();
    const terms = uncoveredTerms.filter((term) => {
        const normalized = term.trim().toLowerCase();
        const termTokens = tokenize(normalized);
        const alreadyPresent = aspect.query.toLowerCase().includes(normalized)
            || (termTokens.size > 0 && [...termTokens].every((token) => existingTokens.has(token)));
        if (!normalized || alreadyPresent || seen.has(normalized))
            return false;
        seen.add(normalized);
        return true;
    });
    const gap = terms.slice(0, 3).join(" ");
    const angles = ["最新进展", "典型案例", "原理解析", "权威解读", "对比分析"];
    const angle = angles.find((candidate) => !aspect.query.includes(candidate));
    if (!gap && !angle)
        return null;
    const suffix = [gap, angle].filter(Boolean).join(" ");
    const refined = makeAspect(`${aspect.id}r${round}`, `${aspect.label}·补盲${round}`, `${aspect.query} ${suffix}`);
    return refined.query === aspect.query ? null : refined;
}
export function makeAspect(id, label, query) {
    const boundedQuery = limitQuery(query);
    return { id, label, query: boundedQuery, keywords: [...tokenize(boundedQuery)] };
}
function limitQuery(query) {
    const trimmed = query.trim();
    if (trimmed.length <= 100)
        return trimmed;
    const bounded = trimmed.slice(0, 100);
    const last = bounded.charCodeAt(bounded.length - 1);
    return (last >= 0xd800 && last <= 0xdbff ? bounded.slice(0, -1) : bounded).trimEnd();
}
/** 显式多主题切分：中文逗号/顿号/问号/分号/换行 + "vs"/"和"/"与"/"以及"/"还是" */
function splitExplicit(query) {
    const raw = query.split(/[，,、？?；;\n]+|(?:以及|还是|vs|和|与)/gi).map((s) => s.trim()).filter(Boolean);
    return raw.length > 0 ? raw : [query];
}
