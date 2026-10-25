// The output side of the privacy gate. The query is masked before it leaves
// the machine (compliance.go); what comes back is another way in: a result's
// title or snippet can carry a phone number, an ID card, a key or a red-line
// term, and every derived view — the claim matrix, the temporal tree, the
// multimodal cards, the Markdown briefing, the agent envelope, the MCP
// payload — is built from it.
//
// The pass is mandatory and independent of the input gate:
//   - a citation whose text hits a red-line term is dropped, and the drop is
//     counted by term (the term is policy, not the result's content);
//   - personal data and secrets in every kept field are replaced with
//     [KIND] markers by magpie's own finders (internal/redact), with no
//     placeholder kept for Restore: what the page and an agent see cannot be
//     turned back into the value;
//   - the same pass runs over the derived strings, so a value copied out of
//     a citation cannot survive in a summary the citation no longer carries.
package searchdet

import (
	"strings"

	"github.com/yetone/magpie/internal/redact"
)

// OutputGuardPolicy names the pass in the audit the page and MCP payloads
// carry. It is magpie's redaction engine with the same red-line list the
// input gate uses; "AstrLink Guard" is the behaviour the research report
// asked for, not a second engine.
const OutputGuardPolicy = "magpie-redact + red-line guard"

// OutputGuardAudit is what the output side did to one investigation. It
// never holds a masked value: kinds and terms are the findings' names, not
// their contents.
type OutputGuardAudit struct {
	Policy         string         `json:"policy"`
	Scanned        int            `json:"scanned"`
	Kept           int            `json:"kept"`
	Dropped        int            `json:"dropped"`
	MaskedFields   int            `json:"maskedFields"`
	MaskedValues   int            `json:"maskedValues"`
	Kinds          map[string]int `json:"kinds,omitempty"`
	RedlineHits    map[string]int `json:"redlineHits,omitempty"`
	DroppedDomains []string       `json:"droppedDomains,omitempty"`
}

func (a *OutputGuardAudit) addKinds(kinds map[string]int, n int) {
	if n == 0 {
		return
	}
	a.MaskedFields++
	a.MaskedValues += n
	if a.Kinds == nil {
		a.Kinds = map[string]int{}
	}
	for kind, c := range kinds {
		a.Kinds[kind] += c
	}
}

func (a *OutputGuardAudit) drop(term, url string) {
	a.Dropped++
	if a.RedlineHits == nil {
		a.RedlineHits = map[string]int{}
	}
	a.RedlineHits[term]++
	if d := domainOf(url); d != "" && !containsString(a.DroppedDomains, d) {
		a.DroppedDomains = append(a.DroppedDomains, d)
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// sanitizeOutput is the field-level pass: personal data and secrets go as
// [KIND] markers. The options are the same on every field, so a value masked
// in one is masked in the next.
func sanitizeOutput(s string) (string, map[string]int, int) {
	out, hits := redact.Sanitize(s, redact.Options{Secrets: true, Personal: true})
	if len(hits) == 0 {
		return s, nil, 0
	}
	kinds := map[string]int{}
	for _, h := range hits {
		kinds[h.Kind]++
	}
	return out, kinds, len(hits)
}

// safeSearchTerm is an entity lifted out of evidence, as it may be sent to a
// provider: one that hits a red-line term is refused, one that carries
// personal data is refused too (searching its [PHONE] marker would either
// waste a request or echo the marker into somebody's index). An entity with
// nothing to mask is returned as written.
func safeSearchTerm(s string, terms []string) string {
	s = strings.TrimSpace(s)
	if s == "" || redlineHit(s, terms) != "" {
		return ""
	}
	if _, _, n := sanitizeOutput(s); n > 0 {
		return ""
	}
	return s
}

// guardCitations drops what hits a red-line term and masks what is kept.
// Dropping happens before masking so the audit's redlineHits never confuses
// a policy term with a PII kind.
func guardCitations(cites []Citation, terms []string, audit *OutputGuardAudit) []Citation {
	kept := make([]Citation, 0, len(cites))
	for _, c := range cites {
		audit.Scanned++
		if term := redlineHit(c.Title+" "+c.Snippet+" "+c.URL, terms); term != "" {
			audit.drop(term, c.URL)
			continue
		}
		var kinds map[string]int
		var n int
		c.Title, kinds, n = sanitizeOutput(c.Title)
		audit.addKinds(kinds, n)
		c.Snippet, kinds, n = sanitizeOutput(c.Snippet)
		audit.addKinds(kinds, n)
		c.URL, kinds, n = sanitizeOutput(c.URL)
		audit.addKinds(kinds, n)
		c.Domain, kinds, n = sanitizeOutput(c.Domain)
		audit.addKinds(kinds, n)
		c.SourceName, kinds, n = sanitizeOutput(c.SourceName)
		audit.addKinds(kinds, n)
		c.Channel, kinds, n = sanitizeOutput(c.Channel)
		audit.addKinds(kinds, n)
		c.PublishedAt, kinds, n = sanitizeOutput(c.PublishedAt)
		audit.addKinds(kinds, n)
		kept = append(kept, c)
	}
	audit.Kept = len(kept)
	return kept
}

// guardCards applies the same rules to multimodal checks: a card whose text
// hits a red-line term is dropped whole (an OCR excerpt or a weather note is
// not a citation that can be half-kept), the rest is masked.
func guardCards(cards []MultimodalCard, terms []string, audit *OutputGuardAudit) []MultimodalCard {
	kept := make([]MultimodalCard, 0, len(cards))
	for _, c := range cards {
		var all strings.Builder
		all.WriteString(c.Title + " " + c.Summary + " " + c.TechDetail + " " + c.SourceURL)
		for _, k := range c.Keyframes {
			all.WriteString(" " + k.Desc)
		}
		if term := redlineHit(all.String(), terms); term != "" {
			audit.drop(term, c.SourceURL)
			continue
		}
		kept = append(kept, sanitizeCard(c, audit))
	}
	return kept
}

// sanitizeCard is the field-level pass over one multimodal card. guardCards
// uses it for an investigation; SanitizeCard uses it for the upload
// endpoint, which returns a card outside any investigation.
func sanitizeCard(c MultimodalCard, audit *OutputGuardAudit) MultimodalCard {
	var kinds map[string]int
	var n int
	c.Icon, kinds, n = sanitizeOutput(c.Icon)
	audit.addKinds(kinds, n)
	c.Title, kinds, n = sanitizeOutput(c.Title)
	audit.addKinds(kinds, n)
	c.Provider, kinds, n = sanitizeOutput(c.Provider)
	audit.addKinds(kinds, n)
	c.Status, kinds, n = sanitizeOutput(c.Status)
	audit.addKinds(kinds, n)
	c.Badge, kinds, n = sanitizeOutput(c.Badge)
	audit.addKinds(kinds, n)
	c.Summary, kinds, n = sanitizeOutput(c.Summary)
	audit.addKinds(kinds, n)
	c.TechDetail, kinds, n = sanitizeOutput(c.TechDetail)
	audit.addKinds(kinds, n)
	c.SourceURL, kinds, n = sanitizeOutput(c.SourceURL)
	audit.addKinds(kinds, n)
	for i := range c.Keyframes {
		c.Keyframes[i].TS, kinds, n = sanitizeOutput(c.Keyframes[i].TS)
		audit.addKinds(kinds, n)
		c.Keyframes[i].Desc, kinds, n = sanitizeOutput(c.Keyframes[i].Desc)
		audit.addKinds(kinds, n)
	}
	return c
}

// SanitizeCard masks personal data and secrets in a card that is returned on
// its own, as the image upload does. An upload never meets the red-line
// check (there is no query), but what it shows is the same kind of content
// a search result carries.
func SanitizeCard(c MultimodalCard) MultimodalCard {
	var audit OutputGuardAudit
	return sanitizeCard(c, &audit)
}

// SanitizeText masks personal data and secrets in text that is stored or
// shown outside an investigation: a saved watch's query and title, its
// summary. The same finders and options as every guarded field.
func SanitizeText(s string) string {
	out, _, _ := sanitizeOutput(s)
	return out
}

// guardString masks one derived field.
func guardString(s string, audit *OutputGuardAudit) string {
	out, kinds, n := sanitizeOutput(s)
	audit.addKinds(kinds, n)
	return out
}

func guardStrings(xs []string, audit *OutputGuardAudit) {
	for i := range xs {
		xs[i] = guardString(xs[i], audit)
	}
}

// guardAny walks a fusion row's values: strings masked, slices and maps
// walked, numbers and booleans left as they are.
func guardAny(v interface{}, audit *OutputGuardAudit) interface{} {
	switch x := v.(type) {
	case string:
		return guardString(x, audit)
	case map[string]interface{}:
		for k, val := range x {
			x[k] = guardAny(val, audit)
		}
		return x
	case []interface{}:
		for i := range x {
			x[i] = guardAny(x[i], audit)
		}
		return x
	}
	return v
}

func guardRows(rows []map[string]string, audit *OutputGuardAudit) {
	for _, row := range rows {
		for k, v := range row {
			row[k] = guardString(v, audit)
		}
	}
}

func guardAnyRows(rows []map[string]interface{}, audit *OutputGuardAudit) {
	for _, row := range rows {
		for k, v := range row {
			row[k] = guardAny(v, audit)
		}
	}
}

// guardDerived masks everything built from the citations after they were
// guarded. The second pass is cheap and closes the copy path (a summary
// that quoted a snippet before the citation was masked, a trajectory that
// echoed the query).
func guardDerived(inv *Investigation, audit *OutputGuardAudit) {
	inv.ExecutiveSummary = guardString(inv.ExecutiveSummary, audit)
	inv.ModeNote = guardString(inv.ModeNote, audit)
	inv.MarkdownBriefing = guardString(inv.MarkdownBriefing, audit)
	inv.AgentEnvelopeXML = guardString(inv.AgentEnvelopeXML, audit)
	for i := range inv.ClaimMatrix {
		inv.ClaimMatrix[i].Claim = guardString(inv.ClaimMatrix[i].Claim, audit)
		inv.ClaimMatrix[i].Fact = guardString(inv.ClaimMatrix[i].Fact, audit)
		inv.ClaimMatrix[i].Status = guardString(inv.ClaimMatrix[i].Status, audit)
		inv.ClaimMatrix[i].SourceURL = guardString(inv.ClaimMatrix[i].SourceURL, audit)
	}
	for i := range inv.TemporalTree {
		inv.TemporalTree[i].Timestamp = guardString(inv.TemporalTree[i].Timestamp, audit)
		inv.TemporalTree[i].Stage = guardString(inv.TemporalTree[i].Stage, audit)
		inv.TemporalTree[i].EdgeType = guardString(inv.TemporalTree[i].EdgeType, audit)
		inv.TemporalTree[i].Title = guardString(inv.TemporalTree[i].Title, audit)
		inv.TemporalTree[i].Detail = guardString(inv.TemporalTree[i].Detail, audit)
		inv.TemporalTree[i].URL = guardString(inv.TemporalTree[i].URL, audit)
	}
	guardStrings(inv.Debate.Pro, audit)
	guardStrings(inv.Debate.Con, audit)
	inv.Debate.JudgeSummary = guardString(inv.Debate.JudgeSummary, audit)
	for i := range inv.Trajectory {
		inv.Trajectory[i].OriginAgent = guardString(inv.Trajectory[i].OriginAgent, audit)
		inv.Trajectory[i].ModeID = guardString(inv.Trajectory[i].ModeID, audit)
		inv.Trajectory[i].Gateway = guardString(inv.Trajectory[i].Gateway, audit)
		inv.Trajectory[i].Params = guardString(inv.Trajectory[i].Params, audit)
		inv.Trajectory[i].ReturnSummary = guardString(inv.Trajectory[i].ReturnSummary, audit)
		inv.Trajectory[i].TimingBreakdown = guardString(inv.Trajectory[i].TimingBreakdown, audit)
		inv.Trajectory[i].TopURL = guardString(inv.Trajectory[i].TopURL, audit)
		inv.Trajectory[i].TopDomain = guardString(inv.Trajectory[i].TopDomain, audit)
	}
	guardRows(inv.FusionNetwork.Origins, audit)
	guardRows(inv.FusionNetwork.SubQueries, audit)
	guardRows(inv.FusionNetwork.Claims, audit)
	guardAnyRows(inv.FusionNetwork.Gateways, audit)
	guardAnyRows(inv.FusionNetwork.EvidenceNodes, audit)
	for _, e := range inv.FusionNetwork.Edges {
		for k, v := range e {
			e[k] = guardAny(v, audit)
		}
	}
}

// guardEvidence is the first half of the pass: it runs before the verdict,
// so a citation that carries a red-line term cannot move a claim, a trust
// score or a multimodal check. The returned audit is finished by guardOutput
// once every derived view exists.
func guardEvidence(inv *Investigation, terms []string) *OutputGuardAudit {
	audit := &OutputGuardAudit{Policy: OutputGuardPolicy}
	if len(terms) == 0 {
		audit.Scanned = len(inv.Citations)
		audit.Kept = len(inv.Citations)
		return audit
	}
	inv.Citations = guardCitations(inv.Citations, terms, audit)
	return audit
}

// guardOutput is the second half: multimodal cards (built from the guarded
// citations but carrying OCR, weather and DOM text of their own) and every
// string derived from the evidence. It writes the audit the page and the
// MCP payload carry.
func guardOutput(inv *Investigation, terms []string, audit *OutputGuardAudit) {
	if len(terms) == 0 {
		inv.OutputGuard = *audit
		return
	}
	inv.MultimodalCards = guardCards(inv.MultimodalCards, terms, audit)
	guardDerived(inv, audit)
	inv.AbstainReason = guardString(inv.AbstainReason, audit)
	if audit.Dropped > 0 && len(inv.Citations) == 0 {
		note := "输出侧合规过滤丢弃了 " + itoa(audit.Dropped) + " 条命中红线的来源。"
		if inv.AbstainReason == "" {
			inv.AbstainReason = note
		} else {
			inv.AbstainReason += " " + note
		}
	}
	inv.OutputGuard = *audit
}
