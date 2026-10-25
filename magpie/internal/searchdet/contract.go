// Package searchdet is MolaSearch's real investigation engine: it runs a
// query through live web channels (keyless aggregators, the search APIs the
// user set up, Tencent Cloud WSA when keyed), scores and folds what comes back,
// and builds the verdict, citations, temporal tree and trajectories the
// Detective page renders. Nothing here is scripted: an unseen query goes to
// the network and the answer is built from what returns.
package searchdet

import (
	"time"

	"github.com/yetone/magpie/internal/usmf"
)

// SearchModeItem is one of the 30 search modes (M01~M30) the catalog serves.
type SearchModeItem struct {
	ID            string   `json:"id"`
	Slug          string   `json:"slug"`
	Family        string   `json:"family"`
	NameZh        string   `json:"nameZh"`
	NameEn        string   `json:"nameEn"`
	Topology      string   `json:"topology"`
	LatencyBudget string   `json:"latencyBudget"`
	Suppliers     []string `json:"suppliers"`
}

// Citation is one live result: where it came from, what channel found it and
// how much the domain is to be believed.
type Citation struct {
	Title          string `json:"title"`
	URL            string `json:"url"`
	Domain         string `json:"domain"`
	SourceName     string `json:"sourceName"`
	Channel        string `json:"channel"`
	AuthorityLevel int    `json:"authorityLevel"`
	PublishedAt    string `json:"publishedAt"`
	Snippet        string `json:"snippet"`
}

// ClaimMatrixRow is one claim of the query held against what was found.
type ClaimMatrixRow struct {
	Claim     string `json:"claim"`
	Fact      string `json:"fact"`
	Status    string `json:"status"`
	SourceURL string `json:"sourceUrl"`
	CiteIndex int    `json:"citeIndex"`
}

// TemporalTreeNode is a dated stage of how the topic evolved, from dates
// extracted out of what the channels returned.
type TemporalTreeNode struct {
	Timestamp string `json:"timestamp"`
	Stage     string `json:"stage"`
	EdgeType  string `json:"edgeType"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	URL       string `json:"url"`
}

// KeyframeItem is one frame of a video contact sheet.
type KeyframeItem struct {
	TS      string `json:"ts"`
	Desc    string `json:"desc"`
	Anomaly bool   `json:"anomaly"`
}

// MultimodalCard is what one modality check found: the weather at a place,
// an uploaded image's fingerprints, a video's danmaku, a page's a11y tree.
type MultimodalCard struct {
	Icon       string         `json:"icon"`
	Title      string         `json:"title"`
	Provider   string         `json:"provider"`
	Status     string         `json:"status"`
	Badge      string         `json:"badge"`
	Summary    string         `json:"summary"`
	TechDetail string         `json:"techDetail"`
	SourceURL  string         `json:"sourceUrl"`
	Keyframes  []KeyframeItem `json:"keyframes,omitempty"`
}

// TrajectoryStep is one real network step the engine took, with its own
// measured latency and what it brought back.
type TrajectoryStep struct {
	OriginAgent string `json:"originAgent"`
	ModeID      string `json:"modeId"`
	Gateway     string `json:"gateway"`
	// ChannelID is the provider the step asked, so a route's flow is this
	// run's own measurement rather than a name matched later.
	ChannelID       string `json:"channelId,omitempty"`
	Params          string `json:"params"`
	StartOffsetMs   int    `json:"startOffsetMs"`
	EndOffsetMs     int    `json:"endOffsetMs"`
	DurationMs      int    `json:"durationMs"`
	PctRedact       int    `json:"pctRedact"`
	PctPlan         int    `json:"pctPlan"`
	PctLocal        int    `json:"pctLocal"`
	PctNet          int    `json:"pctNet"`
	PctRerank       int    `json:"pctRerank"`
	TimingBreakdown string `json:"timingBreakdown"`
	RawHits         int    `json:"rawHits"`
	DedupedHits     int    `json:"dedupedHits"`
	CollapsedSpam   int    `json:"collapsedSpam"`
	ReturnSummary   string `json:"returnSummary"`
	TopURL          string `json:"topUrl"`
	TopDomain       string `json:"topDomain"`
}

// StakeoutJob is a saved watch: a query re-checked on a schedule, its last
// snapshot kept so the next check can diff against it.
type StakeoutJob struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Query         string `json:"query"`
	Cron          string `json:"cron"`
	SearchParams  string `json:"searchParams"`
	LastCheckedAt string `json:"lastCheckedAt"`
	DiffDetected  bool   `json:"diffDetected"`
	StatusBadge   string `json:"statusBadge"`
	LatestDiff    string `json:"latestDiff"`
	SourceURL     string `json:"sourceUrl"`

	// persisted snapshot fields (not all rendered by the page)
	SimHash    uint64 `json:"-"`
	TopDomains string `json:"-"`
	Interval   int    `json:"-"`
}

// DBBackendInfo is one RAG backend the ragstore serves.
type DBBackendInfo struct {
	ID          string `json:"id"`
	Icon        string `json:"icon"`
	Name        string `json:"name"`
	EngineType  string `json:"engineType"`
	Description string `json:"description"`
	DSN         string `json:"dsn"`
	RecordCount int    `json:"recordCount"`
	AvgLatency  string `json:"avgLatency"`
	// Reachable is the result of this status call's real probe. AvgLatency
	// stays the human string it has always been; a page that needs a
	// boolean (a backend that is down reads "unavailable") reads this.
	Reachable    bool   `json:"reachable"`
	Capabilities string `json:"capabilities"`
}

// CanonicalMemoryRecord is one verified fact as every backend stores it. It
// is an alias for the shared USMF type so code that speaks the engine's
// wire types and code that speaks the store's are the same type.
type CanonicalMemoryRecord = usmf.CanonicalMemoryRecord

// RedactAudit is what the privacy guard did to the query before it left.
type RedactAudit struct {
	MaskedQuery      string   `json:"maskedQuery"`
	MaskedCount      int      `json:"maskedCount"`
	Placeholders     []string `json:"placeholders"`
	ComplianceStatus string   `json:"complianceStatus"`
	BlockedReason    string   `json:"blockedReason,omitempty"`
}

// Investigation is the whole answer of one investigate call. The field names
// are the Detective page's contract; every value comes from this run.
type Investigation struct {
	TraceID  string `json:"traceId"`
	Query    string `json:"query"`
	ModeID   string `json:"modeId"`
	ModeName string `json:"modeName"`
	// OriginAgent names who asked for this run — the Detective page or an
	// MCP client's own name — so a result can be traced to its caller.
	OriginAgent      string      `json:"originAgent"`
	Verdict          string      `json:"verdict"`
	TrustScore       int         `json:"trustScore"`
	PrimaryEngine    string      `json:"primaryEngine"`
	ActiveModeBadge  string      `json:"activeModeBadge"`
	ModeNote         string      `json:"modeNote"`
	ExecutiveSummary string      `json:"executiveSummary"`
	RedactAudit      RedactAudit `json:"redactAudit"`
	// OutputGuard is what the result-side pass did: red-line results dropped,
	// personal data masked in every kept field, and the same again in each
	// view built from them.
	OutputGuard        OutputGuardAudit   `json:"outputGuard"`
	TotalLatencyMs     int                `json:"totalLatencyMs"`
	ChannelHits        map[string]int     `json:"channelHits"`
	CollapsedSpamCount int                `json:"collapsedSpamCount"`
	MarkdownBriefing   string             `json:"markdownBriefing,omitempty"`
	AgentEnvelopeXML   string             `json:"agentEnvelopeXml,omitempty"`
	Citations          []Citation         `json:"citations"`
	ClaimMatrix        []ClaimMatrixRow   `json:"claimMatrix"`
	TemporalTree       []TemporalTreeNode `json:"temporalTree"`
	MultimodalCards    []MultimodalCard   `json:"multimodalCards"`
	Debate             Debate             `json:"debate"`
	Trajectory         []TrajectoryStep   `json:"trajectory"`
	FusionNetwork      FusionNetwork      `json:"fusionNetwork"`
	AbstainReason      string             `json:"abstainReason,omitempty"`
	// Answer is the synthesized line over the cited evidence. Status says
	// whether one was produced ("ok"), the evidence could not answer
	// ("insufficient"), synthesis is off ("disabled") or the call failed.
	Answer Answer `json:"answer"`
	// NoPersist says this run must not be written to the memory store: the
	// zero-retention mode (M24) promises nothing is kept.
	NoPersist bool `json:"noPersist"`
	// Report is the pre-rendered report composed after the run: the
	// connection graph, evidence map, claim cards and timeline the page and
	// the export render. It carries no value the output gate did not pass.
	Report      ReportModel `json:"report"`
	GeneratedAt string      `json:"generatedAt"`
}

// Debate is the adversarial pro/con reading of the evidence.
type Debate struct {
	Pro          []string `json:"pro"`
	Con          []string `json:"con"`
	JudgeSummary string   `json:"judgeSummary"`
}

// FusionNetwork is the content graph the page draws: what was asked, what
// ran, what was found and what was concluded.
type FusionNetwork struct {
	Origins       []map[string]string      `json:"origins"`
	SubQueries    []map[string]string      `json:"subQueries"`
	Gateways      []map[string]interface{} `json:"gateways"`
	EvidenceNodes []map[string]interface{} `json:"evidenceNodes"`
	Claims        []map[string]string      `json:"claims"`
	// Edges are the co-occurrence links the graph modes (M07, M24) ask for:
	// two evidence nodes an entity or query term appears in both of.
	Edges []map[string]interface{} `json:"edges,omitempty"`
}

// channelRun is one channel's measured contribution to a run.
type channelRun struct {
	Name      string
	ChannelID string
	Query     string
	Latency   time.Duration
	// At is when this channel's request started, on the wall clock, so the
	// trajectory can place it on the investigation's own timeline.
	At      time.Time
	RawHits int
	Err     error
	TopCite *Citation
}
