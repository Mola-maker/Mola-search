/**
 * 全量对齐《元宝搜索 SearchPro API 文档》(cloud.tencent.com/document/product/1806/121811)
 * 接口域名: wsa.tencentcloudapi.com  Action: SearchPro  Version: 2025-05-08
 */

/** Page 字段：接口返回的 Pages[] 为 JSON 字符串，解析后结构如下 */
export interface SearchPage {
  /** 网页标题 */
  title?: string;
  /** 发布时间，如 2025-10-20 10:00:00 */
  date?: string;
  /** 网页 URL */
  url?: string;
  /** 标准摘要（无 Query 定制） */
  passage?: string;
  /** 动态摘要（针对 Query 定制） */
  content?: string;
  /** 来源站点域名，如 qq.com */
  site?: string;
  /** 相关性得分 0~1 */
  score?: number;
  /** 权威等级 0~5（仅旗舰版返回） */
  authority_level?: number;
  /** 图片列表 0~10 张 */
  pics?: Array<{ caption?: string; origin_url?: string }>;
  favicon?: string;
  /** 主站延伸链接（仅旗舰版） */
  deeplinks?: Array<{ title?: string; url?: string }>;
  /** 所属子查询 / 覆盖方面（插件内补充） */
  aspects?: string[];
  /** 来源轮次（插件内补充） */
  round?: number;
}

export interface SearchRequest {
  /** 搜索词，必需 */
  query: string;
  /** 搜索模式：0 公网全量、1 优质权威垂源（旗舰）、2 混合（旗舰） */
  mode?: number;
  /** 指定/排除站点，如 "qq.com" 或 "exclude:qq.com|sohu.com"（最多排除 5 个） */
  site?: string;
  /** 返回条数：10/20/30/40/50 */
  cnt?: number;
  /** 行业定向：gov / news / acad / finance */
  industry?: string;
  /** 时效：d / m / y / d[1-30] / m[1-12] / y[1-5] / yyyy-mm-dd / yyyy-mm-dd,yyyy-mm-dd */
  freshness?: string;
  /** 旗舰版：返回主站延伸子链接 */
  deeplinks?: boolean;
}

export interface SearchResponse {
  query: string;
  pages: SearchPage[];
  version: string;
  msg?: string;
  requestId: string;
}

export interface WsaCallStats {
  latencyMs: number;
  attempts: number;
  cacheHit: boolean;
}

export class WsaError extends Error {
  constructor(
    message: string,
    public readonly code: string,
    public readonly httpStatus?: number,
    public readonly requestId?: string,
  ) {
    super(message);
    this.name = "WsaError";
  }
}

/** 文档规定的取值约束 —— 全量编码，超限即拒绝 */
export const WSA_CONSTRAINTS = {
  cnt: [10, 20, 30, 40, 50] as const,
  industries: ["gov", "news", "acad", "finance"] as const,
  modes: [0, 1, 2] as const,
  freshnessRe:
    /^(d|m|y|d([1-9]|[12][0-9]|30)|m([1-9]|1[0-2])|y[1-5]|\d{4}-\d{2}-\d{2}|\d{4}-\d{2}-\d{2},\d{4}-\d{2}-\d{2})$/,
  maxSiteExcludes: 5,
  maxQueryLen: 100,
};
