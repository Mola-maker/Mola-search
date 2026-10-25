// DeepSeek Harness (dsh / Cordis) plugin seam for the MolaSearch gateway.
//
// The gateway's HTTP API is on magpie web's port, 3430 by default, not the
// LLM gateway's 3425. The investigate body carries only the fields the
// server decodes; unknown fields are ignored.
export interface MolaSearchConfig {
  endpoint?: string;
  defaultMode?: string;
  webKey?: string;
}

export const name = "molasearch-detective";

export function apply(ctx: any, config: MolaSearchConfig = {}) {
  const endpoint = config.endpoint || "http://127.0.0.1:3430";
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  // magpie web signs requests with the magpie_web_<port> cookie, set by the
  // ?k= link in its startup log; a caller that has the key can send it here.
  if (config.webKey) headers["Cookie"] = `magpie_web_3430=${config.webKey}`;

  ctx.provide("web.search", async (query: string, opts: Record<string, any> = {}) => {
    const res = await fetch(`${endpoint}/api/detective/investigate`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        query,
        modeId: opts.modeId || config.defaultMode || "M11",
        debate: !!opts.debate,
        enableCU: !!opts.enableCU,
        pageUrl: opts.pageUrl || "",
      }),
    });
    return await res.json();
  });
}
