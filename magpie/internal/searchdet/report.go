// The pre-rendered investigation report. An investigation already carries
// every piece a reader needs (citations, claim rows, a temporal tree, a fusion
// graph), but each of those is a raw wire shape. This file turns a completed
// run into one composed model: the connection graph, the evidence map, the
// claim cards and the timeline, with a section marked empty when this run
// really found nothing for it.
//
// The model never invents a node, a link or a date. Graph edges come from a
// claim's own CiteIndex/SourceURL or from the co-occurrence edges the run
// measured; a timeline event exists only when a source carried a date that
// parseDate/extractDates can read. A run with no evidence produces empty
// sections, not placeholder content.
package searchdet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Report section ids. The page draws them in this order.
const (
	ReportSectionSummary  = "summary"
	ReportSectionGraph    = "graph"
	ReportSectionEvidence = "evidence"
	ReportSectionClaims   = "claims"
	ReportSectionTimeline = "timeline"
)

// reportGraphSourceLimit keeps the drawn graph readable. Sources past it stay
// in the evidence map, and ReportGraph.Omitted says how many did.
const reportGraphSourceLimit = 12

// ReportModel is one investigation composed for display and export. Every
// string in it was either read from the guarded investigation or is a label
// the model itself owns; no raw query or pre-guard citation is copied in.
type ReportModel struct {
	ReportID         string `json:"reportId"`
	TraceID          string `json:"traceId"`
	GeneratedAt      string `json:"generatedAt"`
	Query            string `json:"query"`
	ModeID           string `json:"modeId"`
	ModeName         string `json:"modeName"`
	Verdict          string `json:"verdict"`
	TrustScore       int    `json:"trustScore"`
	ExecutiveSummary string `json:"executiveSummary"`
	AbstainReason    string `json:"abstainReason,omitempty"`
	PrimaryEngine    string `json:"primaryEngine"`
	TotalLatencyMs   int    `json:"totalLatencyMs"`
	// NoPersist is true for a zero-retention run and for a blocked one: the
	// report is still shown to the caller, but SaveReport refuses to keep it.
	NoPersist      bool                  `json:"noPersist"`
	Sections       []ReportSection       `json:"sections"`
	Graph          ReportGraph           `json:"graph"`
	Evidence       []ReportEvidence      `json:"evidence"`
	EvidenceGroups []ReportEvidenceGroup `json:"evidenceGroups"`
	Claims         []ReportClaim         `json:"claims"`
	Timeline       []ReportTimelineEvent `json:"timeline"`
}

// ReportSection says whether one part of the report has real content. The
// page shows its own empty-state sentence instead of drawing a blank card.
type ReportSection struct {
	ID    string `json:"id"`
	Empty bool   `json:"empty"`
	Count int    `json:"count"`
}

// ReportGraph is the connection graph: one node per drawn source and claim,
// and only the links the run itself established.
type ReportGraph struct {
	Nodes   []ReportGraphNode `json:"nodes"`
	Edges   []ReportGraphEdge `json:"edges"`
	Empty   bool              `json:"empty"`
	Omitted int               `json:"omitted,omitempty"`
}

// ReportGraphNode is a source or a claim in the graph.
type ReportGraphNode struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // "source" or "claim"
	Label string `json:"label"`
	Sub   string `json:"sub,omitempty"`
	URL   string `json:"url,omitempty"`
	Badge string `json:"badge,omitempty"`
}

// ReportGraphEdge is a link the run established: a claim citing a source, or
// two sources sharing tokens the channels returned.
type ReportGraphEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"` // "cites" or "cooccurrence"
	Label  string `json:"label,omitempty"`
	Weight int    `json:"weight,omitempty"`
}

// ReportEvidence is one real citation in the evidence map.
type ReportEvidence struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	URL            string `json:"url,omitempty"`
	Domain         string `json:"domain,omitempty"`
	SourceName     string `json:"sourceName,omitempty"`
	Channel        string `json:"channel,omitempty"`
	AuthorityLevel int    `json:"authorityLevel"`
	PublishedAt    string `json:"publishedAt,omitempty"`
	Snippet        string `json:"snippet,omitempty"`
}

// ReportEvidenceGroup maps the evidence by the channel that returned it. A
// source with no channel is in the group whose Key is empty.
type ReportEvidenceGroup struct {
	Key         string   `json:"key"`
	Count       int      `json:"count"`
	EvidenceIDs []string `json:"evidenceIds"`
}

// ReportClaim is one row of the claim matrix, with the source node it really
// points at when that source is in the graph.
type ReportClaim struct {
	ID         string `json:"id"`
	Claim      string `json:"claim"`
	Fact       string `json:"fact"`
	Status     string `json:"status"`
	SourceURL  string `json:"sourceUrl,omitempty"`
	EvidenceID string `json:"evidenceId,omitempty"`
	CiteIndex  int    `json:"citeIndex,omitempty"`
}

// ReportTimelineEvent is one dated mention taken from a source. Date is
// always a real date the source or its text carried.
type ReportTimelineEvent struct {
	Date       string `json:"date"`
	Stage      string `json:"stage,omitempty"`
	Title      string `json:"title"`
	Detail     string `json:"detail,omitempty"`
	URL        string `json:"url,omitempty"`
	EvidenceID string `json:"evidenceId,omitempty"`
}

// BuildReport composes a guarded investigation into the pre-rendered report.
// It is deterministic: the same investigation produces the same report id and
// the same model, so reopening a saved run does not re-render anything new.
func BuildReport(inv Investigation) ReportModel {
	// A blocked run never masked its query before refusing to send it, so the
	// masked-copy field can still hold the user's raw text. Run every query
	// through the same finders before it becomes part of a report or export.
	query := strings.TrimSpace(reportText(inv.RedactAudit.MaskedQuery))
	if query == "" {
		query = reportText(inv.Query)
	}
	r := ReportModel{
		TraceID:          inv.TraceID,
		GeneratedAt:      inv.GeneratedAt,
		Query:            query,
		ModeID:           inv.ModeID,
		ModeName:         inv.ModeName,
		Verdict:          inv.Verdict,
		TrustScore:       inv.TrustScore,
		ExecutiveSummary: reportText(inv.ExecutiveSummary),
		AbstainReason:    reportText(inv.AbstainReason),
		PrimaryEngine:    reportText(inv.PrimaryEngine),
		TotalLatencyMs:   inv.TotalLatencyMs,
		NoPersist:        inv.NoPersist || inv.RedactAudit.ComplianceStatus == "BLOCKED_REDLINE",
	}
	r.ReportID = reportIDFor(inv.TraceID, query, inv.GeneratedAt)

	sources, sourceIndex := reportSources(inv.Citations)
	r.Evidence = sources
	r.EvidenceGroups = reportEvidenceGroups(sources)
	r.Claims = reportClaims(inv.ClaimMatrix, sources, sourceIndex)
	r.Graph = reportGraph(sources, r.Claims, inv.FusionNetwork.Edges)
	r.Timeline = reportTimeline(inv.Citations)

	r.Sections = []ReportSection{
		{ID: ReportSectionSummary, Empty: r.ExecutiveSummary == "" && r.Verdict == "", Count: reportCount(r.ExecutiveSummary != "" || r.Verdict != "")},
		{ID: ReportSectionGraph, Empty: len(r.Graph.Nodes) == 0, Count: len(r.Graph.Nodes)},
		{ID: ReportSectionEvidence, Empty: len(r.Evidence) == 0, Count: len(r.Evidence)},
		{ID: ReportSectionClaims, Empty: len(r.Claims) == 0, Count: len(r.Claims)},
		{ID: ReportSectionTimeline, Empty: len(r.Timeline) == 0, Count: len(r.Timeline)},
	}
	return r
}

// ReportSectionOf returns one section by id; the page uses it for the empty
// state without walking the slice.
func ReportSectionOf(r ReportModel, id string) ReportSection {
	for _, s := range r.Sections {
		if s.ID == id {
			return s
		}
	}
	return ReportSection{ID: id, Empty: true}
}

func reportCount(ok bool) int {
	if ok {
		return 1
	}
	return 0
}

// reportIDFor is stable for one run. The trace id is unique per run; a run
// without one (a test fixture, an imported envelope) gets a deterministic
// digest instead of a clock reading.
func reportIDFor(traceID, query, generatedAt string) string {
	if t := strings.TrimSpace(traceID); t != "" {
		return "rpt_" + t
	}
	sum := sha256.Sum256([]byte(query + "\x00" + generatedAt))
	return "rpt_" + hex.EncodeToString(sum[:8])
}

// reportText applies the same finders the output gate uses. A guarded run is
// already masked; this makes the report's own copies safe when a caller
// builds one from an unguarded fixture.
func reportText(s string) string {
	out, _, _ := sanitizeOutput(s)
	return out
}

// reportSources copies the real citations into evidence rows, remembering
// which citation index became which node.
func reportSources(cites []Citation) ([]ReportEvidence, map[int]string) {
	out := make([]ReportEvidence, 0, len(cites))
	index := make(map[int]string, len(cites))
	for i, c := range cites {
		id := fmt.Sprintf("src:%d", i)
		index[i] = id
		out = append(out, ReportEvidence{
			ID:             id,
			Title:          reportText(c.Title),
			URL:            reportHTTPURL(reportText(c.URL)),
			Domain:         reportText(c.Domain),
			SourceName:     reportText(c.SourceName),
			Channel:        reportText(c.Channel),
			AuthorityLevel: c.AuthorityLevel,
			PublishedAt:    reportText(c.PublishedAt),
			Snippet:        reportText(c.Snippet),
		})
	}
	return out, index
}

// reportEvidenceGroups maps evidence by the channel that returned it, largest
// group first. It is a grouping of rows that exist, not a new claim.
func reportEvidenceGroups(ev []ReportEvidence) []ReportEvidenceGroup {
	byKey := map[string][]string{}
	for _, e := range ev {
		byKey[e.Channel] = append(byKey[e.Channel], e.ID)
	}
	groups := make([]ReportEvidenceGroup, 0, len(byKey))
	for key, ids := range byKey {
		groups = append(groups, ReportEvidenceGroup{Key: key, Count: len(ids), EvidenceIDs: ids})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Key < groups[j].Key
	})
	return groups
}

// reportClaims copies the claim rows and resolves, only from what the row
// itself carries (CiteIndex, then SourceURL), the source node it points at.
func reportClaims(rows []ClaimMatrixRow, sources []ReportEvidence, sourceIndex map[int]string) []ReportClaim {
	out := make([]ReportClaim, 0, len(rows))
	byURL := map[string]string{}
	for _, e := range sources {
		if e.URL != "" {
			byURL[normalizeURL(e.URL)] = e.ID
		}
	}
	for _, row := range rows {
		if strings.TrimSpace(row.Claim) == "" && strings.TrimSpace(row.Fact) == "" {
			continue
		}
		c := ReportClaim{
			ID:        fmt.Sprintf("claim:%d", len(out)),
			Claim:     reportText(row.Claim),
			Fact:      reportText(row.Fact),
			Status:    reportText(row.Status),
			SourceURL: reportHTTPURL(reportText(row.SourceURL)),
			CiteIndex: row.CiteIndex,
		}
		if row.CiteIndex > 0 {
			if id, ok := sourceIndex[row.CiteIndex-1]; ok {
				c.EvidenceID = id
			}
		}
		if c.EvidenceID == "" && c.SourceURL != "" {
			if id, ok := byURL[normalizeURL(c.SourceURL)]; ok {
				c.EvidenceID = id
			}
		}
		out = append(out, c)
	}
	return out
}

// reportGraph builds the drawn graph. Sources past the layout limit are
// counted in Omitted; an edge pointing at one of them is not drawn, because
// a link to a node that is not there would be a lie.
func reportGraph(ev []ReportEvidence, claims []ReportClaim, fusionEdges []map[string]interface{}) ReportGraph {
	g := ReportGraph{}
	drawn := len(ev)
	omitted := 0
	if drawn > reportGraphSourceLimit {
		omitted = drawn - reportGraphSourceLimit
		drawn = reportGraphSourceLimit
	}
	g.Omitted = omitted
	for i := 0; i < drawn; i++ {
		e := ev[i]
		sub := e.Channel
		if sub == "" {
			sub = e.Domain
		}
		g.Nodes = append(g.Nodes, ReportGraphNode{
			ID: e.ID, Kind: "source", Label: e.Title, Sub: sub, URL: e.URL,
			Badge: fmt.Sprintf("L%d", e.AuthorityLevel),
		})
	}
	for _, c := range claims {
		g.Nodes = append(g.Nodes, ReportGraphNode{
			ID: c.ID, Kind: "claim", Label: c.Claim, Sub: c.Status, Badge: c.Status,
		})
	}
	seen := map[string]bool{}
	add := func(e ReportGraphEdge) {
		key := e.From + "\x00" + e.To + "\x00" + e.Kind
		if seen[key] {
			return
		}
		seen[key] = true
		g.Edges = append(g.Edges, e)
	}
	for _, c := range claims {
		if c.EvidenceID == "" {
			continue
		}
		if !graphHasNode(g.Nodes, c.EvidenceID) {
			continue
		}
		add(ReportGraphEdge{From: c.ID, To: c.EvidenceID, Kind: "cites", Label: c.Status})
	}
	// Co-occurrence edges are index-based over the run's citations. Draw only
	// the ones whose two endpoints are drawn nodes.
	for _, raw := range fusionEdges {
		from, okFrom := reportInt(raw["from"])
		to, okTo := reportInt(raw["to"])
		if !okFrom || !okTo || from < 0 || to < 0 || from >= drawn || to >= drawn {
			continue
		}
		weight := 0
		if w, ok := reportInt(raw["weight"]); ok {
			weight = w
		}
		label := ""
		if s, ok := raw["label"].(string); ok {
			label = reportText(s)
		}
		add(ReportGraphEdge{
			From: ev[from].ID, To: ev[to].ID, Kind: "cooccurrence",
			Label: label, Weight: weight,
		})
	}
	g.Empty = len(g.Nodes) == 0
	return g
}

func graphHasNode(nodes []ReportGraphNode, id string) bool {
	for _, n := range nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func reportInt(v interface{}) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		i := int(x)
		if float64(i) == x {
			return i, true
		}
	}
	return 0, false
}

// reportTimeline keeps only dates a source really carried: a date in its own
// title or snippet, or its published date. An undated run gets an empty
// timeline; no "today" node is manufactured for it.
func reportTimeline(cites []Citation) []ReportTimelineEvent {
	type dated struct {
		at time.Time
		ev ReportTimelineEvent
	}
	var found []dated
	seen := map[string]bool{}
	for i, c := range cites {
		var (
			at    time.Time
			stage string
		)
		if refs := extractDates(c.Title+" "+c.Snippet, 1); len(refs) > 0 {
			at, stage = refs[0].Time, refs[0].Stage
		} else if t, ok := parseDate(c.PublishedAt); ok {
			at, stage = t, t.Format("2006-01-02")
		} else {
			continue
		}
		date := at.Format("2006-01-02")
		id := fmt.Sprintf("src:%d", i)
		if seen[date+"\x00"+id] {
			continue
		}
		seen[date+"\x00"+id] = true
		found = append(found, dated{at: at, ev: ReportTimelineEvent{
			Date:       date,
			Stage:      reportText(stage),
			Title:      reportText(c.Title),
			Detail:     reportText(firstRunesOf(c.Snippet, 160)),
			URL:        reportHTTPURL(reportText(c.URL)),
			EvidenceID: id,
		}})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].at.After(found[j].at) })
	out := make([]ReportTimelineEvent, 0, len(found))
	for _, d := range found {
		out = append(out, d.ev)
		if len(out) == 16 {
			break
		}
	}
	return out
}

// reportHTTPURL keeps an absolute http(s) address for a link and returns ""
// otherwise, so a report never renders a javascript: or data: href.
func reportHTTPURL(raw string) string {
	s := strings.TrimSpace(raw)
	// A masked marker inside the address means the gate found something in
	// it: keep the text out of a link rather than link to a broken URL.
	if strings.ContainsAny(s, "[]") {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return s
}
