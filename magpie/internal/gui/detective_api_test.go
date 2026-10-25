package gui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/searchdet"
)

// TestDetectiveReportRoutesServeTheSavedModel holds the three read routes to
// what BuildReport wrote: the list, one model, and the Markdown/HTML export.
// A report that is not on disk is a 404, not an empty model.
func TestDetectiveReportRoutesServeTheSavedModel(t *testing.T) {
	t.Setenv("MOLASEARCH_DATA", t.TempDir())
	rep := searchdet.BuildReport(searchdet.Investigation{
		TraceID:     "trc_routes",
		Query:       "a question",
		ModeID:      "M01",
		Verdict:     "UNVERIFIED",
		GeneratedAt: "2026-01-02T03:04:05Z",
		Citations: []searchdet.Citation{{
			Title: "A source", URL: "https://example.com/a", Channel: "exa",
			AuthorityLevel: 3, PublishedAt: "2026-01-01",
		}},
	})
	if err := searchdet.SaveReport(rep); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	detectiveRoutes(mux)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	list := get("/api/detective/reports")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), rep.ReportID) {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
	one := get("/api/detective/reports/" + rep.ReportID)
	if one.Code != http.StatusOK || !strings.Contains(one.Body.String(), `"reportId":"`+rep.ReportID+`"`) {
		t.Fatalf("one: %d %s", one.Code, one.Body)
	}
	md := get("/api/detective/reports/" + rep.ReportID + "/export?format=md")
	if md.Code != http.StatusOK || !strings.HasPrefix(md.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("markdown export: %d %q %s", md.Code, md.Header().Get("Content-Type"), md.Body)
	}
	if !strings.Contains(md.Body.String(), "https://example.com/a") || !strings.Contains(md.Body.String(), "2026-01-01") {
		t.Fatalf("markdown export lost the source:\n%s", md.Body)
	}
	htmlOut := get("/api/detective/reports/" + rep.ReportID + "/export?format=html")
	if htmlOut.Code != http.StatusOK || !strings.HasPrefix(htmlOut.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("html export: %d %q", htmlOut.Code, htmlOut.Header().Get("Content-Type"))
	}
	if !strings.Contains(htmlOut.Body.String(), "<!doctype html>") || !strings.Contains(htmlOut.Body.String(), "https://example.com/a") {
		t.Fatalf("html export is not a standalone page with the source:\n%s", htmlOut.Body)
	}
	if missing := get("/api/detective/reports/rpt_missing"); missing.Code != http.StatusNotFound {
		t.Fatalf("missing report = %d, want 404", missing.Code)
	}
}
