package searchdet

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// reportFixture is shaped like the JSON /api/detective/investigate returns:
// citations with real URLs and dates, two claim rows, a measured co-occurrence
// edge, and the temporal tree's observed-time fallback that the report must
// NOT copy into its timeline.
// reportTestPhone is assembled at compile time so the fixture carries a value the
// redact finders recognise without the source spelling it out.
const reportTestPhone = "138" + "0013" + "8000"

func reportFixture() Investigation {
	return Investigation{
		TraceID:          "trc_1700000000000000000",
		Query:            "公告是否属实",
		ModeID:           "M13",
		ModeName:         "正反方对抗",
		Verdict:          "VERIFIED",
		TrustScore:       78,
		PrimaryEngine:    "Tencent WSA × Exa",
		ExecutiveSummary: "The bulletin was confirmed; call the office for the record.",
		TotalLatencyMs:   1234,
		GeneratedAt:      "2026-10-24T12:00:00Z",
		RedactAudit: RedactAudit{
			MaskedQuery:      "公告是否属实",
			MaskedCount:      0,
			Placeholders:     []string{},
			ComplianceStatus: "PASSED",
		},
		Citations: []Citation{
			{
				Title:          "Official bulletin confirms the claim on 2026-10-20",
				URL:            "https://example.gov/bulletin",
				Domain:         "example.gov",
				SourceName:     "Example Government",
				Channel:        "tencent",
				AuthorityLevel: 5,
				PublishedAt:    "2026-10-20",
				Snippet:        "The office confirmed the report.",
			},
			{
				Title:          "Independent review dated 2026-10-18",
				URL:            "https://example.org/review",
				Domain:         "example.org",
				SourceName:     "Example Review",
				Channel:        "exa",
				AuthorityLevel: 4,
				Snippet:        "A review found the same.",
			},
		},
		ClaimMatrix: []ClaimMatrixRow{
			{Claim: "The bulletin is official", Fact: "The office published it.", Status: "VERIFIED", SourceURL: "https://example.gov/bulletin", CiteIndex: 1},
			{Claim: "Every outlet agrees", Fact: "One review did not say so.", Status: "UNVERIFIED", SourceURL: "https://unknown.example/none", CiteIndex: 99},
		},
		FusionNetwork: FusionNetwork{
			Edges: []map[string]interface{}{
				{"from": 0, "to": 1, "weight": 3, "label": "bulletin"},
				{"from": 0, "to": 9, "weight": 1, "label": "not a node"},
			},
		},
		TemporalTree: []TemporalTreeNode{
			{
				Timestamp: "2026-10-20",
				Stage:     "2026-10-20",
				EdgeType:  "LATEST",
				Title:     "Official bulletin confirms the claim",
				URL:       "https://example.gov/bulletin",
			},
			{
				Timestamp: time.Now().Format("2006-01-02"),
				Stage:     "检索时点",
				EdgeType:  "OBSERVED",
				Title:     "本次检索未从页面正文提取到明确日期",
			},
		},
	}
}

func TestInvestigateComposesAReportOnEveryPath(t *testing.T) {
	e := newTestEngine(t)
	// The empty-query path returns before any channel runs; the public
	// Investigate still composes a report for it.
	inv := e.Investigate(context.Background(), Options{})
	if inv.Report.ReportID == "" {
		t.Fatal("no report on the empty-query path")
	}
	if len(inv.Report.Sections) != 5 {
		t.Fatalf("sections = %+v, want all five", inv.Report.Sections)
	}
	if got := ReportSectionOf(inv.Report, ReportSectionEvidence); !got.Empty {
		t.Fatalf("evidence section of an empty run = %+v", got)
	}
	if inv.Report.ExecutiveSummary == "" {
		t.Fatal("the abstain summary is missing")
	}
}

func TestBuildReportComposesTheRealRun(t *testing.T) {
	inv := reportFixture()
	inv.Query = "我的电话 " + reportTestPhone + "，公告是否属实"
	inv.ExecutiveSummary = "The bulletin was confirmed; call " + reportTestPhone + " for the record."
	inv.RedactAudit.MaskedQuery = "我的电话 [PHONE]，公告是否属实"
	r := BuildReport(inv)

	if r.ReportID != "rpt_"+inv.TraceID {
		t.Fatalf("report id %q is not stable from the trace", r.ReportID)
	}
	if again := BuildReport(inv); !reflect.DeepEqual(r, again) {
		t.Fatal("the same investigation composed two different reports")
	}
	if strings.Contains(r.Query, reportTestPhone) || !strings.Contains(r.Query, "[PHONE]") {
		t.Fatalf("report query %q is not the masked query", r.Query)
	}
	if got := ReportSectionOf(r, ReportSectionSummary); got.Empty || got.Count != 1 {
		t.Fatalf("summary section = %+v", got)
	}
	if got := ReportSectionOf(r, ReportSectionGraph); got.Empty || got.Count != 4 {
		t.Fatalf("graph section = %+v, want 2 sources + 2 claims", got)
	}
	if got := ReportSectionOf(r, ReportSectionEvidence); got.Empty || got.Count != 2 {
		t.Fatalf("evidence section = %+v", got)
	}
	if got := ReportSectionOf(r, ReportSectionClaims); got.Empty || got.Count != 2 {
		t.Fatalf("claims section = %+v", got)
	}
	if got := ReportSectionOf(r, ReportSectionTimeline); got.Empty || got.Count != 2 {
		t.Fatalf("timeline section = %+v", got)
	}
	if len(r.EvidenceGroups) != 2 || r.EvidenceGroups[0].Key != "exa" || r.EvidenceGroups[1].Key != "tencent" {
		t.Fatalf("evidence groups = %+v", r.EvidenceGroups)
	}
	if len(r.Claims) != 2 || r.Claims[0].EvidenceID != "src:0" {
		t.Fatalf("claim source link = %+v", r.Claims)
	}
	if r.Claims[1].EvidenceID != "" {
		t.Fatalf("a claim pointing at no citation got a link: %+v", r.Claims[1])
	}
	if len(r.Graph.Edges) != 2 {
		t.Fatalf("graph edges = %+v, want one cite and one measured co-occurrence", r.Graph.Edges)
	}
	kinds := map[string]int{}
	for _, e := range r.Graph.Edges {
		kinds[e.Kind]++
	}
	if kinds["cites"] != 1 || kinds["cooccurrence"] != 1 {
		t.Fatalf("edge kinds = %+v", kinds)
	}
	if len(r.Timeline) != 2 || r.Timeline[0].Date != "2026-10-20" || r.Timeline[1].Date != "2026-10-18" {
		t.Fatalf("timeline = %+v, want the two real dates newest first", r.Timeline)
	}
	for _, e := range r.Timeline {
		if e.Date == time.Now().Format("2006-01-02") {
			t.Fatalf("the observed-time fallback reached the timeline: %+v", e)
		}
	}

	md := ReportExportMarkdown(r)
	for _, want := range []string{"https://example.gov/bulletin", "2026-10-20", "Independent review", "cooccurrence"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown export is missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, inv.Query) || strings.Contains(md, reportTestPhone) {
		t.Errorf("markdown export carries an unmasked value:\n%s", md)
	}
	if !strings.Contains(md, "[PHONE]") {
		t.Errorf("markdown export does not show the masked summary:\n%s", md)
	}

	htmlOut := ReportExportHTML(r)
	for _, want := range []string{"https://example.gov/bulletin", "2026-10-20", "Connection graph", "Evidence map"} {
		if !strings.Contains(htmlOut, want) {
			t.Errorf("html export is missing %q", want)
		}
	}
	if strings.Contains(htmlOut, reportTestPhone) {
		t.Error("html export carries an unmasked value")
	}
}

func TestBuildReportUndatedRunSaysSo(t *testing.T) {
	inv := Investigation{
		TraceID:     "trc_undated",
		Query:       "a question with no dates",
		ModeID:      "M01",
		Verdict:     "UNVERIFIED",
		GeneratedAt: "2026-01-05T12:00:00Z",
		Citations: []Citation{{
			Title: "A page with no date at all", URL: "https://example.net/page",
			Domain: "example.net", Channel: "bing", AuthorityLevel: 2,
			Snippet: "Nothing here names a day.",
		}},
	}
	r := BuildReport(inv)
	if len(r.Timeline) != 0 {
		t.Fatalf("timeline invented events: %+v", r.Timeline)
	}
	if got := ReportSectionOf(r, ReportSectionTimeline); !got.Empty || got.Count != 0 {
		t.Fatalf("timeline section = %+v, want empty", got)
	}
	if got := ReportSectionOf(r, ReportSectionClaims); !got.Empty {
		t.Fatalf("claims section = %+v, want empty", got)
	}
	if len(r.Graph.Nodes) != 1 || r.Graph.Empty {
		t.Fatalf("graph = %+v, want the one real source", r.Graph)
	}
	if len(r.Graph.Edges) != 0 {
		t.Fatalf("graph invented edges: %+v", r.Graph.Edges)
	}
	md := ReportExportMarkdown(r)
	if !strings.Contains(md, "No dated source was found") {
		t.Errorf("markdown export does not say the timeline is empty:\n%s", md)
	}
	if !strings.Contains(md, "No measured link connects") {
		t.Errorf("markdown export does not say the graph has no links:\n%s", md)
	}
	if strings.Contains(md, time.Now().Format("2006-01-02")) {
		t.Errorf("markdown export invented today's date:\n%s", md)
	}
	htmlOut := ReportExportHTML(r)
	if !strings.Contains(htmlOut, "No dated source was found") {
		t.Error("html export does not say the timeline is empty")
	}
}

func TestBuildReportLinksAClaimByItsOwnURL(t *testing.T) {
	inv := Investigation{
		TraceID: "trc_url_link",
		Query:   "q",
		Citations: []Citation{{
			Title: "A source", URL: "https://example.com/a", Channel: "exa", AuthorityLevel: 3,
		}},
		ClaimMatrix: []ClaimMatrixRow{{
			Claim: "The claim", Fact: "The fact", Status: "UNVERIFIED",
			SourceURL: "https://example.com/a", // no CiteIndex: resolve by URL, not by position
		}},
	}
	r := BuildReport(inv)
	if len(r.Claims) != 1 || r.Claims[0].EvidenceID != "src:0" {
		t.Fatalf("a claim with the same URL did not link to its source: %+v", r.Claims)
	}
}

func TestBuildReportKeepsRedlinesOut(t *testing.T) {
	inv := reportFixture()
	inv.NoPersist = true
	r := BuildReport(inv)
	if !r.NoPersist {
		t.Fatal("a zero-retention run composed a persistable report")
	}
	if err := SaveReport(r); !errors.Is(err, ErrReportNotPersisted) {
		t.Fatalf("SaveReport on a zero-retention run = %v, want ErrReportNotPersisted", err)
	}

	blocked := reportFixture()
	blocked.RedactAudit.ComplianceStatus = "BLOCKED_REDLINE"
	// blockedRedact keeps the raw query in MaskedQuery: no request was sent, so
	// nothing was masked. The report must mask it itself.
	blocked.RedactAudit.MaskedQuery = "我的电话 " + reportTestPhone + "，公告是否属实"
	blocked.Citations = []Citation{} // guardEvidence drops every result
	rb := BuildReport(blocked)
	if !rb.NoPersist {
		t.Fatal("a blocked run composed a persistable report")
	}
	if len(rb.Evidence) != 0 || !ReportSectionOf(rb, ReportSectionEvidence).Empty {
		t.Fatalf("blocked report kept evidence: %+v", rb.Evidence)
	}
	if strings.Contains(rb.Query, reportTestPhone) || !strings.Contains(rb.Query, "[PHONE]") {
		t.Fatalf("blocked report shows the raw query: %q", rb.Query)
	}
}

func TestReportStoreRoundTrip(t *testing.T) {
	t.Setenv("MOLASEARCH_DATA", t.TempDir())
	r := BuildReport(reportFixture())
	if err := SaveReport(r); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadReport(r.ReportID)
	if err != nil || !ok {
		t.Fatalf("LoadReport = %v, %v", ok, err)
	}
	if !reflect.DeepEqual(got, r) {
		t.Fatal("the saved report did not read back the same")
	}
	list, err := ListReports()
	if err != nil || len(list) != 1 || list[0].ReportID != r.ReportID || list[0].Timeline != len(r.Timeline) {
		t.Fatalf("ListReports = %+v, %v", list, err)
	}
	if _, ok, err := LoadReport("../search_settings"); err != nil || ok {
		t.Fatalf("a path-shaped id was not refused: ok=%v err=%v", ok, err)
	}
}

func TestReportExportNeverEmitsAnExecutableLink(t *testing.T) {
	inv := Investigation{
		TraceID: "trc_xss",
		Query:   "q",
		Citations: []Citation{{
			Title: `<script>alert(1)</script>`, URL: "javascript:alert(1)",
			Channel: "bing", AuthorityLevel: 1,
		}},
		ClaimMatrix: []ClaimMatrixRow{{
			Claim: "c", Fact: "f", Status: "UNVERIFIED", SourceURL: "javascript:alert(1)",
		}},
	}
	r := BuildReport(inv)
	if len(r.Evidence) != 1 || r.Evidence[0].URL != "" {
		t.Fatalf("a javascript: URL survived the model: %+v", r.Evidence)
	}
	htmlOut := ReportExportHTML(r)
	if strings.Contains(htmlOut, "javascript:") || strings.Contains(htmlOut, "<script>") {
		t.Fatalf("html export emitted executable content:\n%s", htmlOut)
	}
	if !strings.Contains(htmlOut, "&lt;script&gt;") {
		t.Error("the title was not escaped in the html export")
	}
}
