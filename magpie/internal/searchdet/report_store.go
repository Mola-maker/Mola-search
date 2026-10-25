// Persistence and export for the pre-rendered report. A report is kept under
// DataDir()/reports with the trace-derived id BuildReport gives it, so the
// page can reopen the composed report instead of re-rendering the wire shape.
// A zero-retention or blocked run is never written: SaveReport refuses it.
package searchdet

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ErrReportNotPersisted is returned by SaveReport for a run whose NoPersist
// flag is set. Callers treat it as "shown, not kept", not as a failure.
var ErrReportNotPersisted = errors.New("this run is not persisted")

// reportIDPattern keeps a report id a safe single path element.
var reportIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ReportSummary is one row of the saved-report list.
type ReportSummary struct {
	ReportID    string `json:"reportId"`
	TraceID     string `json:"traceId"`
	Query       string `json:"query"`
	Verdict     string `json:"verdict"`
	TrustScore  int    `json:"trustScore"`
	GeneratedAt string `json:"generatedAt"`
	Citations   int    `json:"citations"`
	Claims      int    `json:"claims"`
	Timeline    int    `json:"timeline"`
}

// ReportsDir is where composed reports live, beside the settings file.
func ReportsDir() string { return filepath.Join(DataDir(), "reports") }

// SaveReport writes one report through a temp file and a rename, so a reader
// never sees half a file. A zero-retention or blocked run is refused.
func SaveReport(r ReportModel) error {
	if r.NoPersist {
		return ErrReportNotPersisted
	}
	if !reportIDPattern.MatchString(r.ReportID) {
		return fmt.Errorf("invalid report id %q", r.ReportID)
	}
	dir := ReportsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, r.ReportID+".json")
	tmp, err := os.CreateTemp(dir, ".report-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// LoadReport reads one saved report. A missing file is (zero, false, nil); a
// file that cannot be read or parsed is an error, because "unknown" is not
// the same as "no report".
func LoadReport(id string) (ReportModel, bool, error) {
	if !reportIDPattern.MatchString(id) {
		return ReportModel{}, false, nil
	}
	b, err := os.ReadFile(filepath.Join(ReportsDir(), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return ReportModel{}, false, nil
	}
	if err != nil {
		return ReportModel{}, false, err
	}
	var r ReportModel
	if err := json.Unmarshal(b, &r); err != nil {
		return ReportModel{}, false, fmt.Errorf("%s: %w", id, err)
	}
	return r, true, nil
}

// ListReports returns the saved reports, newest first. A file that cannot be
// read is left out rather than failing the whole list; the caller still sees
// the others.
func ListReports() ([]ReportSummary, error) {
	entries, err := os.ReadDir(ReportsDir())
	if errors.Is(err, os.ErrNotExist) {
		return []ReportSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]ReportSummary, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(ReportsDir(), e.Name()))
		if err != nil {
			continue
		}
		var r ReportModel
		if err := json.Unmarshal(b, &r); err != nil {
			continue
		}
		out = append(out, ReportSummary{
			ReportID:    r.ReportID,
			TraceID:     r.TraceID,
			Query:       r.Query,
			Verdict:     r.Verdict,
			TrustScore:  r.TrustScore,
			GeneratedAt: r.GeneratedAt,
			Citations:   len(r.Evidence),
			Claims:      len(r.Claims),
			Timeline:    len(r.Timeline),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GeneratedAt != out[j].GeneratedAt {
			return out[i].GeneratedAt > out[j].GeneratedAt
		}
		return out[i].ReportID > out[j].ReportID
	})
	return out, nil
}

// ReportExportMarkdown is the shareable Markdown built from the model alone.
// Links are written only for an http(s) URL the model kept.
func ReportExportMarkdown(r ReportModel) string {
	var b strings.Builder
	title := mdInline(r.Query)
	if title == "" {
		title = "investigation"
	}
	fmt.Fprintf(&b, "# MolaSearch report: %s\n\n", title)
	fmt.Fprintf(&b, "- Verdict: %s\n", mdInline(r.Verdict))
	fmt.Fprintf(&b, "- Trust: %d/100\n", r.TrustScore)
	fmt.Fprintf(&b, "- Mode: %s %s\n", mdInline(r.ModeID), mdInline(r.ModeName))
	fmt.Fprintf(&b, "- Generated: %s\n", mdInline(r.GeneratedAt))
	fmt.Fprintf(&b, "- Trace: %s\n", mdInline(r.TraceID))
	fmt.Fprintf(&b, "- Report: %s\n", mdInline(r.ReportID))
	fmt.Fprintf(&b, "- Engine: %s\n", mdInline(r.PrimaryEngine))
	fmt.Fprintf(&b, "- Latency: %d ms\n\n", r.TotalLatencyMs)
	if r.ExecutiveSummary != "" {
		fmt.Fprintf(&b, "> %s\n\n", mdInline(r.ExecutiveSummary))
	}
	if r.AbstainReason != "" {
		fmt.Fprintf(&b, "> Abstained: %s\n\n", mdInline(r.AbstainReason))
	}

	b.WriteString("## Connection graph\n\n")
	if len(r.Graph.Nodes) == 0 {
		b.WriteString("No source or claim nodes were recorded for this run.\n\n")
	} else {
		labels := map[string]string{}
		for _, n := range r.Graph.Nodes {
			labels[n.ID] = n.Label
		}
		for _, n := range r.Graph.Nodes {
			fmt.Fprintf(&b, "- %s: %s%s\n", n.Kind, mdInline(n.Label), mdBadge(n))
		}
		b.WriteString("\n")
		if len(r.Graph.Edges) == 0 {
			b.WriteString("No measured link connects these nodes in this run.\n\n")
		} else {
			for _, e := range r.Graph.Edges {
				from := labels[e.From]
				to := labels[e.To]
				if from == "" {
					from = e.From
				}
				if to == "" {
					to = e.To
				}
				fmt.Fprintf(&b, "- %s -> %s (%s%s)\n", mdInline(from), mdInline(to), e.Kind, mdWeight(e))
			}
			b.WriteString("\n")
		}
		if r.Graph.Omitted > 0 {
			fmt.Fprintf(&b, "%d more sources are in the evidence map below.\n\n", r.Graph.Omitted)
		}
	}

	b.WriteString("## Evidence map\n\n")
	if len(r.Evidence) == 0 {
		b.WriteString("This run returned no sources, so there is no evidence map.\n\n")
	} else {
		for _, g := range r.EvidenceGroups {
			name := g.Key
			if name == "" {
				name = "No channel recorded"
			}
			fmt.Fprintf(&b, "### %s (%d)\n\n", mdInline(name), g.Count)
			for _, id := range g.EvidenceIDs {
				for _, e := range r.Evidence {
					if e.ID != id {
						continue
					}
					line := fmt.Sprintf("L%d", e.AuthorityLevel)
					if e.Domain != "" {
						line += " · " + mdInline(e.Domain)
					}
					if e.PublishedAt != "" {
						line += " · " + mdInline(e.PublishedAt)
					}
					if e.URL != "" {
						fmt.Fprintf(&b, "- [%s](%s) — %s\n", mdLabel(e.Title), e.URL, line)
					} else {
						fmt.Fprintf(&b, "- %s — %s\n", mdInline(e.Title), line)
					}
					if e.Snippet != "" {
						fmt.Fprintf(&b, "  %s\n", mdInline(e.Snippet))
					}
					break
				}
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Claims\n\n")
	if len(r.Claims) == 0 {
		b.WriteString("This run recorded no claim rows.\n\n")
	} else {
		for _, c := range r.Claims {
			fmt.Fprintf(&b, "- **%s** %s", mdInline(c.Status), mdInline(c.Claim))
			if c.SourceURL != "" {
				fmt.Fprintf(&b, " ([source](%s))", c.SourceURL)
			}
			b.WriteString("\n")
			if c.Fact != "" {
				fmt.Fprintf(&b, "  %s\n", mdInline(c.Fact))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("## Timeline\n\n")
	if len(r.Timeline) == 0 {
		b.WriteString("No dated source was found, so there is no timeline.\n")
	} else {
		for _, e := range r.Timeline {
			if e.URL != "" {
				fmt.Fprintf(&b, "- %s — [%s](%s)\n", e.Date, mdLabel(e.Title), e.URL)
			} else {
				fmt.Fprintf(&b, "- %s — %s\n", e.Date, mdInline(e.Title))
			}
		}
	}
	return b.String()
}

// mdInline keeps a value on one line.
func mdInline(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// mdLabel is a value used where a link's text goes, so a bracket in a title
// cannot break the link around it.
func mdLabel(s string) string {
	return strings.NewReplacer("[", "\\[", "]", "\\]").Replace(mdInline(s))
}

func mdBadge(n ReportGraphNode) string {
	if n.Badge == "" {
		return ""
	}
	return " (" + mdInline(n.Badge) + ")"
}

func mdWeight(e ReportGraphEdge) string {
	if e.Weight > 0 {
		return fmt.Sprintf(", weight %d", e.Weight)
	}
	if e.Label != "" {
		return ", " + mdInline(e.Label)
	}
	return ""
}

// ReportExportHTML is a standalone, script-free page built from the model.
// It uses the MolaMaker palette and a single responsive column so a phone
// reads it the same way the window does.
func ReportExportHTML(r ReportModel) string {
	var b strings.Builder
	title := "MolaSearch report"
	if r.Query != "" {
		title += ": " + r.Query
	}
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(title))
	b.WriteString(`<style>
:root{--ink:#163f70;--blue:#246eb6;--pink:#d967a7;--muted:#637c97;--line:#dce7f2;--paper:#fcfdff;--soft:#f3f7fc}
*{box-sizing:border-box}
body{margin:0;background:var(--soft);color:#1f2d3d;font:15px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
main{max-width:960px;margin:0 auto;padding:28px 18px 60px}
h1{font-size:24px;line-height:1.25;color:var(--ink);margin:0 0 6px}
h2{font-size:17px;color:var(--ink);margin:28px 0 10px}
h3{font-size:14px;color:var(--ink);margin:18px 0 8px}
p,li{overflow-wrap:anywhere}
.meta{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:8px;margin:14px 0 0}
.meta div{background:var(--paper);border:1px solid var(--line);border-radius:10px;padding:8px 10px;font-size:12px}
.meta b{display:block;color:var(--muted);font-weight:500;font-size:11px}
.card{background:var(--paper);border:1px solid var(--line);border-radius:14px;padding:14px 16px;margin-top:10px}
.verdict{color:var(--blue);font-weight:600}
.empty{color:var(--muted);font-size:13px;text-align:center;padding:16px 0}
a{color:var(--blue);text-decoration:none}
a:hover{text-decoration:underline}
.tag{display:inline-block;border-radius:999px;padding:1px 8px;font-size:11px;background:var(--soft);border:1px solid var(--line);color:var(--muted);margin-left:6px}
.edge{font-size:13px;color:var(--muted)}
.note{font-size:12px;color:var(--muted)}
@media(max-width:480px){main{padding:18px 12px 40px}h1{font-size:20px}.meta{grid-template-columns:1fr 1fr}}
</style>
</head>
<body><main>
`)
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(r.Query))
	fmt.Fprintf(&b, "<p class=\"verdict\">%s · trust %d/100</p>\n", html.EscapeString(r.Verdict), r.TrustScore)
	b.WriteString("<div class=\"meta\">\n")
	metaRow(&b, "Mode", r.ModeID+" "+r.ModeName)
	metaRow(&b, "Generated", r.GeneratedAt)
	metaRow(&b, "Trace", r.TraceID)
	metaRow(&b, "Report", r.ReportID)
	metaRow(&b, "Engine", r.PrimaryEngine)
	metaRow(&b, "Latency", fmt.Sprintf("%d ms", r.TotalLatencyMs))
	b.WriteString("</div>\n")
	if r.ExecutiveSummary != "" {
		fmt.Fprintf(&b, "<div class=\"card\"><p>%s</p></div>\n", html.EscapeString(r.ExecutiveSummary))
	}
	if r.AbstainReason != "" {
		fmt.Fprintf(&b, "<p class=\"note\">Abstained: %s</p>\n", html.EscapeString(r.AbstainReason))
	}

	b.WriteString("<h2>Connection graph</h2>\n<div class=\"card\">\n")
	if len(r.Graph.Nodes) == 0 {
		b.WriteString("<p class=\"empty\">No source or claim nodes were recorded for this run.</p>\n")
	} else {
		labels := map[string]string{}
		b.WriteString("<ul>\n")
		for _, n := range r.Graph.Nodes {
			labels[n.ID] = n.Label
			badge := ""
			if n.Badge != "" {
				badge = "<span class=\"tag\">" + html.EscapeString(n.Badge) + "</span>"
			}
			if n.URL != "" {
				fmt.Fprintf(&b, "<li>%s: <a href=\"%s\" rel=\"noopener noreferrer\" target=\"_blank\">%s</a>%s</li>\n", html.EscapeString(n.Kind), html.EscapeString(n.URL), html.EscapeString(n.Label), badge)
			} else {
				fmt.Fprintf(&b, "<li>%s: %s%s</li>\n", html.EscapeString(n.Kind), html.EscapeString(n.Label), badge)
			}
		}
		b.WriteString("</ul>\n")
		if len(r.Graph.Edges) == 0 {
			b.WriteString("<p class=\"note\">No measured link connects these nodes in this run.</p>\n")
		} else {
			b.WriteString("<ul class=\"edge\">\n")
			for _, e := range r.Graph.Edges {
				from, to := labels[e.From], labels[e.To]
				if from == "" {
					from = e.From
				}
				if to == "" {
					to = e.To
				}
				fmt.Fprintf(&b, "<li>%s &rarr; %s (%s)</li>\n", html.EscapeString(from), html.EscapeString(to), html.EscapeString(e.Kind))
			}
			b.WriteString("</ul>\n")
		}
		if r.Graph.Omitted > 0 {
			fmt.Fprintf(&b, "<p class=\"note\">%d more sources are in the evidence map below.</p>\n", r.Graph.Omitted)
		}
	}
	b.WriteString("</div>\n")

	b.WriteString("<h2>Evidence map</h2>\n")
	if len(r.Evidence) == 0 {
		b.WriteString("<div class=\"card\"><p class=\"empty\">This run returned no sources, so there is no evidence map.</p></div>\n")
	} else {
		for _, g := range r.EvidenceGroups {
			name := g.Key
			if name == "" {
				name = "No channel recorded"
			}
			fmt.Fprintf(&b, "<h3>%s (%d)</h3>\n<div class=\"card\">\n", html.EscapeString(name), g.Count)
			for _, id := range g.EvidenceIDs {
				for _, e := range r.Evidence {
					if e.ID != id {
						continue
					}
					if e.URL != "" {
						fmt.Fprintf(&b, "<p><a href=\"%s\" rel=\"noopener noreferrer\" target=\"_blank\">%s</a>", html.EscapeString(e.URL), html.EscapeString(e.Title))
					} else {
						fmt.Fprintf(&b, "<p>%s", html.EscapeString(e.Title))
					}
					if e.Domain != "" {
						fmt.Fprintf(&b, " <span class=\"tag\">%s</span>", html.EscapeString(e.Domain))
					}
					fmt.Fprintf(&b, " <span class=\"tag\">L%d</span>", e.AuthorityLevel)
					b.WriteString("</p>\n")
					if e.Snippet != "" {
						fmt.Fprintf(&b, "<p class=\"note\">%s</p>\n", html.EscapeString(e.Snippet))
					}
					break
				}
			}
			b.WriteString("</div>\n")
		}
	}

	b.WriteString("<h2>Claims</h2>\n")
	b.WriteString("<div class=\"card\">\n")
	if len(r.Claims) == 0 {
		b.WriteString("<p class=\"empty\">This run recorded no claim rows.</p>\n")
	} else {
		for _, c := range r.Claims {
			fmt.Fprintf(&b, "<p><b>%s</b> %s", html.EscapeString(c.Status), html.EscapeString(c.Claim))
			if c.SourceURL != "" {
				fmt.Fprintf(&b, " <a href=\"%s\" rel=\"noopener noreferrer\" target=\"_blank\">source</a>", html.EscapeString(c.SourceURL))
			}
			b.WriteString("</p>\n")
			if c.Fact != "" {
				fmt.Fprintf(&b, "<p class=\"note\">%s</p>\n", html.EscapeString(c.Fact))
			}
		}
	}
	b.WriteString("</div>\n")

	b.WriteString("<h2>Timeline</h2>\n<div class=\"card\">\n")
	if len(r.Timeline) == 0 {
		b.WriteString("<p class=\"empty\">No dated source was found, so there is no timeline.</p>\n")
	} else {
		b.WriteString("<ul>\n")
		for _, e := range r.Timeline {
			if e.URL != "" {
				fmt.Fprintf(&b, "<li>%s — <a href=\"%s\" rel=\"noopener noreferrer\" target=\"_blank\">%s</a></li>\n", html.EscapeString(e.Date), html.EscapeString(e.URL), html.EscapeString(e.Title))
			} else {
				fmt.Fprintf(&b, "<li>%s — %s</li>\n", html.EscapeString(e.Date), html.EscapeString(e.Title))
			}
		}
		b.WriteString("</ul>\n")
	}
	b.WriteString("</div>\n</main></body></html>\n")
	return b.String()
}

func metaRow(b *strings.Builder, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "<div><b>%s</b>%s</div>\n", html.EscapeString(label), html.EscapeString(value))
}
