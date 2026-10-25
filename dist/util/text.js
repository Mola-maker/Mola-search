import { mkdirSync, writeFileSync, readFileSync, existsSync, readdirSync, unlinkSync } from "node:fs";
import { join } from "node:path";
/** CJK 感知的轻量分词：拉丁词按字母段切，CJK 取二元组（无需分词库） */
export function tokenize(text) {
    const tokens = new Set();
    const cleaned = (text ?? "").toLowerCase();
    // 拉丁/数字连续段
    const latin = cleaned.match(/[a-z0-9][a-z0-9+#.\-]*/g) ?? [];
    for (const w of latin)
        tokens.add(w);
    // CJK 二元组
    const cjk = cleaned.match(/[\u4e00-\u9fff\u3400-\u4dbf]/g) ?? [];
    for (let i = 0; i + 1 < cjk.length; i++) {
        tokens.add(cjk[i] + cjk[i + 1]);
    }
    if (cjk.length === 1)
        tokens.add(cjk[0]);
    return tokens;
}
export function jaccard(a, b) {
    if (a.size === 0 && b.size === 0)
        return 1;
    if (a.size === 0 || b.size === 0)
        return 0;
    let inter = 0;
    for (const t of a)
        if (b.has(t))
            inter += 1;
    return inter / (a.size + b.size - inter);
}
/** URL 归一化键：去协议/查询串/锚点，统一小写 host+path */
export function urlKey(url) {
    try {
        const u = new URL(url);
        return `${u.host.replace(/^www\./, "")}${u.pathname.replace(/\/+$/, "")}`.toLowerCase();
    }
    catch {
        return url.toLowerCase();
    }
}
export function clamp(x, lo, hi) {
    return Math.max(lo, Math.min(hi, x));
}
const historyIdPattern = /^swarm-[0-9a-z]+-[0-9a-z]{1,3}$/;
const historyFilePattern = /^swarm-[0-9a-z]+-[0-9a-z]{1,3}\.json$/;
function isHistoryId(id) {
    return historyIdPattern.test(id);
}
export class HistoryStore {
    dir;
    constructor(dir) {
        this.dir = dir;
        try {
            mkdirSync(dir, { recursive: true });
        }
        catch {
            /* 读取时按无历史处理；写入错误由 save 抛出 */
        }
    }
    save(rec) {
        if (!isHistoryId(rec.id))
            throw new Error(`无效的历史 ID: ${rec.id}`);
        try {
            writeFileSync(join(this.dir, `${rec.id}.json`), JSON.stringify(rec, null, 2), "utf8");
            this.prune(50);
        }
        catch (error) {
            throw new Error(`保存历史记录失败: ${rec.id}`, { cause: error });
        }
    }
    get(id) {
        if (!isHistoryId(id))
            return null;
        const p = join(this.dir, `${id}.json`);
        if (!existsSync(p))
            return null;
        try {
            return JSON.parse(readFileSync(p, "utf8"));
        }
        catch {
            return null;
        }
    }
    list(limit = 20) {
        try {
            return readdirSync(this.dir)
                .filter((f) => historyFilePattern.test(f))
                .sort()
                .reverse()
                .slice(0, limit)
                .map((f) => {
                const rec = this.get(f.replace(/\.json$/, ""));
                return (rec && {
                    id: rec.id,
                    createdAt: rec.createdAt,
                    query: rec.query,
                    profile: rec.profile,
                    rounds: rec.rounds,
                    uniqueHits: rec.uniqueHits,
                });
            })
                .filter(Boolean);
        }
        catch {
            return [];
        }
    }
    prune(max) {
        const files = readdirSync(this.dir)
            .filter((f) => historyFilePattern.test(f))
            .sort();
        while (files.length > max) {
            const f = files.shift();
            if (f)
                unlinkSync(join(this.dir, f));
        }
    }
}
let counter = 0;
export function newId(prefix) {
    counter = (counter + 1) % 10000;
    return `${prefix}-${Date.now().toString(36)}-${counter.toString(36)}`;
}
