import { createHash, createHmac } from "node:crypto";
function sha256Hex(data) {
    return createHash("sha256").update(data, "utf8").digest("hex");
}
function hmac(key, data) {
    return createHmac("sha256", key).update(data, "utf8").digest();
}
export function tc3Sign(input) {
    const timestamp = input.timestamp ?? Math.floor(Date.now() / 1000);
    const date = new Date(timestamp * 1000).toISOString().slice(0, 10);
    // 1. 拼接规范请求串（POST JSON；CanonicalURI/QueryString 恒为 "/" 与 ""）
    const canonicalHeaders = `content-type:application/json; charset=utf-8\nhost:${input.host}\nx-tc-action:${input.action.toLowerCase()}\n`;
    const signedHeaders = "content-type;host;x-tc-action";
    const hashedPayload = sha256Hex(input.payload);
    const canonicalRequest = [
        "POST",
        "/",
        "",
        canonicalHeaders,
        signedHeaders,
        hashedPayload,
    ].join("\n");
    // 2. 拼接待签名串
    const stringToSign = [
        "TC3-HMAC-SHA256",
        String(timestamp),
        `${date}/${input.service}/tc3_request`,
        sha256Hex(canonicalRequest),
    ].join("\n");
    // 3. 派生签名密钥
    const kDate = hmac(`TC3${input.secretKey}`, date);
    const kService = hmac(kDate, input.service);
    const kSigning = hmac(kService, "tc3_request");
    const signature = createHmac("sha256", kSigning)
        .update(stringToSign, "utf8")
        .digest("hex");
    const authorization = `TC3-HMAC-SHA256 Credential=${input.secretId}/${date}/${input.service}/tc3_request, ` +
        `SignedHeaders=${signedHeaders}, Signature=${signature}`;
    return { authorization, timestamp, date };
}
