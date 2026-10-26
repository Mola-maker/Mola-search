# MolaSearch Detective: agent rules for this plugin

## Core rules
- **Search before claiming.** Verify uncertain facts, versions, venues or
  screenshots through the `molasearch` MCP server (`magpie_investigate`).
- **Cite what you used.** Every factual assertion carries a clickable
  `[Title](url)` from the run's `citations`; each citation names its real
  domain, channel and authority level. Do not cite a page the run did not
  return.
- **Honest abstention.** When `verdict` is `ABSTAIN`, say the evidence was
  insufficient (查无实据 / 暂无法证实). Never fill the gap from memory.
- **Privacy and compliance.** The engine masks PII and blocks the compliance
  word list before any network call. Do not paste a blocked query's content
  elsewhere to work around it.

## Routing by intent
- Code and architecture → `M23` (code forensics), `M02` (hybrid recall), `M30` (ensemble).
- Rumour / fact-check → `M13` (pro/con), `M20` (authority), `M17` (timeline).
- Place / weather → `M25`. Images → `M22`/`M26`. Video → `M27`. Walled-garden pages or devices → `M28`/`M29`.

All modes are the same tool: `magpie_investigate` with a `modeId`. There is
no per-mode MCP tool, no `audience` field and no token budget in the current
API.

## Outputs
`verdict`, `citations[]`, `channelHits`, `trajectory[]`, `temporalTree` and
`agentEnvelopeXml`. Read `channelHits` before claiming which source answered.
