/**
 * 全量对齐《元宝搜索 SearchPro API 文档》(cloud.tencent.com/document/product/1806/121811)
 * 接口域名: wsa.tencentcloudapi.com  Action: SearchPro  Version: 2025-05-08
 */
export class WsaError extends Error {
    code;
    httpStatus;
    requestId;
    constructor(message, code, httpStatus, requestId) {
        super(message);
        this.code = code;
        this.httpStatus = httpStatus;
        this.requestId = requestId;
        this.name = "WsaError";
    }
}
/** 文档规定的取值约束 —— 全量编码，超限即拒绝 */
export const WSA_CONSTRAINTS = {
    cnt: [10, 20, 30, 40, 50],
    industries: ["gov", "news", "acad", "finance"],
    modes: [0, 1, 2],
    freshnessRe: /^(d|m|y|d([1-9]|[12][0-9]|30)|m([1-9]|1[0-2])|y[1-5]|\d{4}-\d{2}-\d{2}|\d{4}-\d{2}-\d{2},\d{4}-\d{2}-\d{2})$/,
    maxSiteExcludes: 5,
    maxQueryLen: 100,
};
