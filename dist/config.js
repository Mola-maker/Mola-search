import { readFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { parse as parseDotenv } from "dotenv";
export const BUILTIN_PROFILES = [
    {
        name: "deep-research",
        description: "默认深搜：公网+混合双模式轮换，5 方面分解，最多 3 轮迭代",
        strategy: "comprehensive",
        maxRounds: 3,
        maxSubqueries: 5,
        searchersPerQuery: 2,
        coverageThreshold: 0.75,
        cnt: 20,
    },
    {
        name: "news-scan",
        description: "新闻扫描：news 行业定向 + 近 30 天时效，快速两轮",
        strategy: "news",
        maxRounds: 2,
        maxSubqueries: 3,
        searchersPerQuery: 2,
        coverageThreshold: 0.7,
        cnt: 20,
        industry: "news",
        freshness: "d30",
    },
    {
        name: "academic-dive",
        description: "学术深挖：acad 行业定向，偏综述/论文/方法/实验四方面",
        strategy: "academic",
        maxRounds: 3,
        maxSubqueries: 4,
        searchersPerQuery: 2,
        coverageThreshold: 0.8,
        cnt: 30,
        industry: "acad",
    },
    {
        name: "finance-watch",
        description: "金融监测：finance 行业定向 + 近 90 天时效",
        strategy: "finance",
        maxRounds: 2,
        maxSubqueries: 4,
        searchersPerQuery: 2,
        coverageThreshold: 0.7,
        cnt: 20,
        industry: "finance",
        freshness: "m3",
    },
    {
        name: "quick-look",
        description: "快速一览：单轮单查询，最快出结果",
        strategy: "quick",
        maxRounds: 1,
        maxSubqueries: 1,
        searchersPerQuery: 1,
        coverageThreshold: 0.5,
        cnt: 10,
    },
];
export const ALL_TOOL_NAMES = [
    "yuanbao_search",
    "swarm_iterative_search",
    "swarm_history",
    "plugin_manage",
];
const CONFIG_FILE_NAMES = [".yuanbao-swarm.json", "yuanbao-swarm.config.json"];
export const FRESHNESS_PATTERN = /^(?:d|m|y|d(?:[1-9]|[12][0-9]|30)|m(?:[1-9]|1[0-2])|y[1-5]|\d{4}-\d{2}-\d{2}|\d{4}-\d{2}-\d{2},\d{4}-\d{2}-\d{2})$/;
function readConfigFile(dir) {
    for (const name of CONFIG_FILE_NAMES) {
        const p = join(dir, name);
        if (!existsSync(p))
            continue;
        try {
            return JSON.parse(readFileSync(p, "utf8"));
        }
        catch (err) {
            throw new Error(`无法读取配置文件 ${p}: ${err.message}`);
        }
    }
    return null;
}
function readDotenv(dir) {
    const path = join(dir, ".env");
    if (!existsSync(path))
        return {};
    try {
        return parseDotenv(readFileSync(path, "utf8"));
    }
    catch (err) {
        throw new Error(`无法加载环境文件 ${path}: ${err.message}`);
    }
}
function numberSetting(name, envValue, fileValue, dflt, rule) {
    const raw = envValue ?? fileValue ?? dflt;
    const value = Number(raw);
    if ((typeof raw === "string" && !raw.trim()) || !Number.isFinite(value) || value < rule.min || (rule.integer && !Number.isInteger(value))) {
        const kind = rule.integer ? "整数" : "数值";
        throw new Error(`配置 ${name} 必须为不小于 ${rule.min} 的${kind}`);
    }
    return value;
}
function booleanSetting(name, envValue, fileValue, dflt) {
    if (envValue === undefined)
        return fileValue ?? dflt;
    const value = envValue.toLowerCase();
    if (["1", "true", "yes", "on"].includes(value))
        return true;
    if (["0", "false", "no", "off"].includes(value))
        return false;
    throw new Error(`配置 ${name} 必须为布尔值（true/false、1/0、yes/no、on/off）`);
}
/** 配置优先级：进程环境变量 > 项目 .env > 配置文件 > 默认值 */
export function loadConfig(cwd = process.cwd()) {
    const dotenv = readDotenv(cwd);
    const env = { ...dotenv, ...process.env };
    const file = readConfigFile(cwd) ?? {};
    const profiles = new Map(BUILTIN_PROFILES.map((profile) => [profile.name, profile]));
    if (file.profiles !== undefined && !Array.isArray(file.profiles)) {
        throw new Error("配置 profiles 必须为数组");
    }
    for (const profile of file.profiles ?? []) {
        if (!profile || typeof profile.name !== "string" || !profile.name.trim()) {
            throw new Error("配置 Profile 的 name 必须为非空字符串");
        }
        profiles.set(profile.name, normalizeProfile({
            ...BUILTIN_PROFILES[0],
            ...profile,
            name: profile.name,
            description: profile.description ?? `自定义 Profile ${profile.name}`,
        }));
    }
    const enabledTools = file.enabledTools ?? [...ALL_TOOL_NAMES];
    if (!Array.isArray(enabledTools))
        throw new Error("配置 enabledTools 必须为数组");
    for (const tool of enabledTools) {
        if (typeof tool !== "string" || !ALL_TOOL_NAMES.includes(tool)) {
            throw new Error(`配置 enabledTools 含未知工具: ${String(tool)}`);
        }
    }
    return {
        secretId: process.env.TENCENTCLOUD_SECRET_ID ??
            process.env.TENCENT_SECRET_ID ??
            dotenv.TENCENTCLOUD_SECRET_ID ??
            dotenv.TENCENT_SECRET_ID ??
            file.secretId ??
            "",
        secretKey: process.env.TENCENTCLOUD_SECRET_KEY ??
            process.env.TENCENT_SECRET_KEY ??
            dotenv.TENCENTCLOUD_SECRET_KEY ??
            dotenv.TENCENT_SECRET_KEY ??
            file.secretKey ??
            "",
        endpoint: env.YUANBAO_ENDPOINT ?? file.endpoint ?? "https://wsa.tencentcloudapi.com",
        qps: numberSetting("YUANBAO_QPS", env.YUANBAO_QPS, file.qps, 5, { min: Number.MIN_VALUE }),
        timeoutMs: numberSetting("YUANBAO_TIMEOUT_MS", env.YUANBAO_TIMEOUT_MS, file.timeoutMs, 15000, { integer: true, min: 1 }),
        maxRetries: numberSetting("YUANBAO_MAX_RETRIES", env.YUANBAO_MAX_RETRIES, file.maxRetries, 2, { integer: true, min: 0 }),
        dryRun: booleanSetting("YUANBAO_DRY_RUN", env.YUANBAO_DRY_RUN, file.dryRun, false),
        enabledTools: [...new Set(enabledTools)],
        profiles: [...profiles.values()],
        dataDir: env.YUANBAO_DATA_DIR ?? file.dataDir ?? join(cwd, ".yuanbao-swarm-data"),
    };
}
/** Profile 字段规范化 + 收敛到文档/引擎约束 */
export function normalizeProfile(raw) {
    const strategies = ["comprehensive", "news", "academic", "finance", "quick"];
    const cnt = [10, 20, 30, 40, 50].includes(Number(raw.cnt)) ? Number(raw.cnt) : 20;
    if (raw.freshness !== undefined && (typeof raw.freshness !== "string" || !FRESHNESS_PATTERN.test(raw.freshness))) {
        throw new Error("Profile freshness 非法：应为 d/m/y、d[1-30]、m[1-12]、y[1-5]、日期或日期区间");
    }
    return {
        name: String(raw.name ?? "unnamed").slice(0, 64),
        description: String(raw.description ?? ""),
        strategy: strategies.includes(String(raw.strategy)) ? String(raw.strategy) : "comprehensive",
        maxRounds: clampInt(raw.maxRounds, 1, 5, 3),
        maxSubqueries: clampInt(raw.maxSubqueries, 1, 8, 5),
        searchersPerQuery: clampInt(raw.searchersPerQuery, 1, 3, 2),
        coverageThreshold: clampNum(raw.coverageThreshold, 0, 1, 0.75),
        cnt,
        industry: raw.industry && ["gov", "news", "acad", "finance"].includes(String(raw.industry)) ? String(raw.industry) : undefined,
        freshness: raw.freshness,
    };
}
function clampInt(value, lo, hi, dflt) {
    const n = Number(value);
    return Number.isFinite(n) ? Math.max(lo, Math.min(hi, Math.round(n))) : dflt;
}
function clampNum(value, lo, hi, dflt) {
    const n = Number(value);
    return Number.isFinite(n) ? Math.max(lo, Math.min(hi, n)) : dflt;
}
