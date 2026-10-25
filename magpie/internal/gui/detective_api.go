// The Detective page's API: the 30-mode catalog, live investigations, the
// provider registry and its gates, image uploads, stakeout watches and the
// agent injection that really writes a skill and an MCP entry into an
// agent's own folders. No provider is privileged: every one can be turned
// off, and the run fails over to the rest.
package gui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/library"
	"github.com/yetone/magpie/internal/ragstore"
	"github.com/yetone/magpie/internal/searchdet"
)

var (
	detOnce     sync.Once
	detEngine   *searchdet.Engine
	detEngineMu sync.Mutex
)

// detectiveEngine is the process's one engine, so the kill-switch and the
// quota survive between requests.
func detectiveEngine() *searchdet.Engine {
	detEngineMu.Lock()
	defer detEngineMu.Unlock()
	detOnce.Do(func() { detEngine = searchdet.NewEngine() })
	return detEngine
}

// detectiveRoutes registers the page's endpoints on the window's mux.
func detectiveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/detective/modes", func(rw http.ResponseWriter, r *http.Request) {
		modes := searchdet.Catalog()
		if modes == nil {
			modes = []searchdet.SearchModeItem{}
		}
		writeJSON(rw, map[string]any{"count": len(modes), "modes": modes})
	})

	mux.HandleFunc("POST /api/detective/investigate", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Query       string `json:"query"`
			DemoID      string `json:"demoId"`
			ModeID      string `json:"modeId"`
			Debate      bool   `json:"debate"`
			EnableCU    bool   `json:"enableCU"`
			ImageBase64 string `json:"imageBase64"`
			ImageName   string `json:"imageName"`
			PageURL     string `json:"pageUrl"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		e := detectiveEngine()
		var img []byte
		if in.ImageBase64 != "" {
			if raw, err := base64.StdEncoding.DecodeString(stripDataURL(in.ImageBase64)); err == nil {
				img = raw
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		inv := e.Investigate(ctx, searchdet.Options{
			Query: in.Query, ModeID: in.ModeID, DemoID: in.DemoID,
			Debate: in.Debate, EnableCU: in.EnableCU,
			ImageName: in.ImageName, ImageData: img, PageURL: in.PageURL,
		})
		persistInvestigation(ctx, inv)
		recordRouteFlows(inv)
		// The composed report is kept beside the run's trace id, so reopening
		// it does not re-render anything. A zero-retention or blocked run is
		// refused by SaveReport and still shown now.
		_ = searchdet.SaveReport(inv.Report)
		writeJSON(rw, inv)
	})

	mux.HandleFunc("GET /api/detective/settings", func(rw http.ResponseWriter, r *http.Request) {
		e := detectiveEngine()
		out := map[string]any{
			"settings": e.Settings(),
			"defaults": searchdet.DefaultSettings(),
			"file":     searchdet.SettingsFile(),
		}
		if err := e.SettingsError(); err != nil {
			out["error"] = err.Error()
		}
		writeJSON(rw, out)
	})

	mux.HandleFunc("POST /api/detective/settings", func(rw http.ResponseWriter, r *http.Request) {
		var in searchdet.Settings
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		// The file is the durable copy; only a save that succeeded is applied,
		// so a restart cannot silently revert to other gates than the page
		// showed.
		if err := searchdet.SaveSettings(in); err != nil {
			fail(rw, err)
			return
		}
		e := detectiveEngine()
		if err := e.SetSettings(in); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"ok": true, "settings": e.Settings()})
	})

	mux.HandleFunc("GET /api/detective/channels", func(rw http.ResponseWriter, r *http.Request) {
		e := detectiveEngine()
		writeJSON(rw, map[string]any{"channels": e.ChannelStatuses()})
	})

	// Saved composed reports: the list the page reopens, one report by id,
	// and the Markdown/HTML export built from the model itself.
	mux.HandleFunc("GET /api/detective/reports", func(rw http.ResponseWriter, r *http.Request) {
		reports, err := searchdet.ListReports()
		if err != nil {
			fail(rw, err)
			return
		}
		if reports == nil {
			reports = []searchdet.ReportSummary{}
		}
		writeJSON(rw, map[string]any{"reports": reports})
	})

	mux.HandleFunc("GET /api/detective/reports/{id}", func(rw http.ResponseWriter, r *http.Request) {
		report, ok, err := searchdet.LoadReport(r.PathValue("id"))
		if err != nil {
			fail(rw, err)
			return
		}
		if !ok {
			http.NotFound(rw, r)
			return
		}
		writeJSON(rw, report)
	})

	mux.HandleFunc("GET /api/detective/reports/{id}/export", func(rw http.ResponseWriter, r *http.Request) {
		report, ok, err := searchdet.LoadReport(r.PathValue("id"))
		if err != nil {
			fail(rw, err)
			return
		}
		if !ok {
			http.NotFound(rw, r)
			return
		}
		switch strings.ToLower(r.URL.Query().Get("format")) {
		case "html", "htm":
			rw.Header().Set("Content-Type", "text/html; charset=utf-8")
			rw.Header().Set("Content-Disposition", `attachment; filename="`+report.ReportID+`.html"`)
			_, _ = io.WriteString(rw, searchdet.ReportExportHTML(report))
		default:
			rw.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			rw.Header().Set("Content-Disposition", `attachment; filename="`+report.ReportID+`.md"`)
			_, _ = io.WriteString(rw, searchdet.ReportExportMarkdown(report))
		}
	})

	mux.HandleFunc("POST /api/detective/channels", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		e := detectiveEngine()
		if err := e.SetChannelEnabled(in.ID, in.Enabled); err != nil {
			fail(rw, err)
			return
		}
		if err := searchdet.SaveSettings(e.Settings()); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"ok": true, "channels": e.ChannelStatuses()})
	})

	mux.HandleFunc("GET /api/detective/state", func(rw http.ResponseWriter, r *http.Request) {
		e := detectiveEngine()
		out := map[string]any{
			"channels": e.ChannelStatuses(),
			"settings": e.Settings(),
			"defaults": searchdet.DefaultSettings(),
			"file":     searchdet.SettingsFile(),
			"envFile":  searchdet.EnvFile(),
		}
		if err := e.SettingsError(); err != nil {
			out["settingsError"] = err.Error()
		}
		writeJSON(rw, out)
	})

	mux.HandleFunc("GET /api/detective/keys", func(rw http.ResponseWriter, r *http.Request) {
		keys := searchdet.KeyStatuses()
		if keys == nil {
			keys = []searchdet.KeyStatus{}
		}
		writeJSON(rw, map[string]any{"keys": keys, "envFile": searchdet.EnvFile()})
	})

	// Upload: multipart form first, then a JSON base64 body. The real
	// fingerprints are computed on the bytes, never on a name.
	mux.HandleFunc("POST /api/detective/upload-image", func(rw http.ResponseWriter, r *http.Request) {
		var (
			name string
			data []byte
		)
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "multipart/form-data") {
			if err := r.ParseMultipartForm(16 << 20); err != nil {
				fail(rw, err)
				return
			}
			f, hdr, err := r.FormFile("image")
			if err != nil {
				fail(rw, err)
				return
			}
			defer f.Close()
			name = hdr.Filename
			data, err = io.ReadAll(io.LimitReader(f, 16<<20))
			if err != nil {
				fail(rw, err)
				return
			}
		} else {
			var in struct {
				Name   string `json:"name"`
				Base64 string `json:"base64"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 24<<20)).Decode(&in); err != nil {
				fail(rw, err)
				return
			}
			raw, err := base64.StdEncoding.DecodeString(stripDataURL(in.Base64))
			if err != nil {
				fail(rw, err)
				return
			}
			name, data = in.Name, raw
		}
		if len(data) == 0 {
			fail(rw, fmt.Errorf("empty image"))
			return
		}
		card, err := searchdet.ImageCard(name, data)
		if err != nil {
			fail(rw, err)
			return
		}
		// This card is returned outside an investigation, so it does not
		// pass the investigation's output gate: mask it here, with the same
		// finders. (The card never carries raw GPS coordinates.)
		card = searchdet.SanitizeCard(card)
		writeJSON(rw, map[string]any{"ok": true, "card": card})
	})

	// Stakeout watches are persisted in the ragstore's SQLite; the check
	// runs the real channels.
	mux.HandleFunc("GET /api/detective/stakeout", func(rw http.ResponseWriter, r *http.Request) {
		jobs, err := stakeoutList(r.Context())
		if err != nil {
			fail(rw, err)
			return
		}
		if jobs == nil {
			jobs = []searchdet.StakeoutJob{}
		}
		writeJSON(rw, map[string]any{"jobs": jobs})
	})

	mux.HandleFunc("POST /api/detective/stakeout", func(rw http.ResponseWriter, r *http.Request) {
		var in searchdet.StakeoutJob
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		// A watch is stored and re-run unattended: it passes the same input
		// gate as an investigate call, so a red-line query is refused and
		// what is kept is the masked query.
		masked, err := searchdet.PrepareWatchQuery(in.Query)
		if err != nil {
			fail(rw, err)
			return
		}
		in.Query = masked
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		if in.ID == "" || in.Title == "" {
			if in.ID == "" {
				in.ID = fmt.Sprintf("stk_%d", time.Now().UnixNano())
			}
			if in.Title == "" {
				in.Title = molaFirstRunes(in.Query, 30)
			}
		}
		if in.Interval <= 0 {
			in.Interval = searchdet.ParseCronInterval(in.Cron)
		}
		if in.StatusBadge == "" {
			in.StatusBadge = "⏳ 待首次巡检"
		}
		in = searchdet.SanitizeWatch(in)
		if err := m.StakeoutSave(r.Context(), in); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"ok": true, "job": in})
	})

	mux.HandleFunc("DELETE /api/detective/stakeout/{id}", func(rw http.ResponseWriter, r *http.Request) {
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		if err := m.StakeoutDelete(r.Context(), r.PathValue("id")); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/detective/stakeout/check", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			JobID string `json:"jobId"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		if in.JobID != "" {
			diff, job, err := stakeoutCheck(ctx, in.JobID)
			if err != nil {
				fail(rw, err)
				return
			}
			writeJSON(rw, map[string]any{"jobs": []searchdet.StakeoutJob{job}, "diff": diff})
			return
		}
		jobs, diffs, err := stakeoutCheckAll(ctx)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"jobs": jobs, "diffs": diffs})
	})

	mux.HandleFunc("POST /api/detective/modes/inject", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Agent   string `json:"agent"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		res, err := injectAgent(in.Agent, in.Enabled)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, res)
	})
}

// persistInvestigation writes what a real run found into the active memory
// backend and teaches the habits table, so the next run starts smarter.
// A run with no citation writes nothing: an empty memory is not evidence.
func persistInvestigation(ctx context.Context, inv searchdet.Investigation) {
	// Zero-retention runs and blocked runs leave nothing behind: the promise
	// is part of the mode, so it is honored before anything touches disk.
	if inv.NoPersist || inv.RedactAudit.ComplianceStatus == "BLOCKED_REDLINE" {
		return
	}
	if len(inv.Citations) == 0 {
		return
	}
	m, err := ragManagerOf()
	if err != nil {
		return
	}
	// The run names who asked for it (the Detective page, or an MCP client's
	// own name), so the memory write in the live feed is attributed to that
	// caller rather than to the page that happened to persist it.
	ctx = ragstore.WithActor(ctx, inv.OriginAgent)
	top := inv.Citations[0]
	var evidence []string
	for i, c := range inv.Citations {
		if i >= 4 {
			break
		}
		evidence = append(evidence, c.Title+" — "+c.Snippet)
	}
	rec := searchdet.CanonicalMemoryRecord{
		MemoryID:       inv.TraceID,
		Topic:          molaFirstRunes(inv.Query, 60),
		Claim:          inv.Query,
		EvidenceText:   strings.Join(evidence, "\n"),
		Verdict:        inv.Verdict,
		RelationEdge:   edgeFor(inv.Verdict),
		ValidFrom:      time.Now().Format(time.RFC3339),
		SourceURL:      top.URL,
		SourceDomain:   top.Domain,
		AuthorityLevel: top.AuthorityLevel,
		EmbeddingModel: m.EmbeddingModel(),
	}
	_ = m.Insert(ctx, []searchdet.CanonicalMemoryRecord{rec})
	_ = m.LearnFromInvestigation(ctx, inv.Query, inv.Verdict, inv.Citations)
}

// edgeFor maps a verdict onto the bi-temporal edge the graph keeps.
func edgeFor(verdict string) string {
	switch verdict {
	case "REFUTED":
		return "CONTRADICTS"
	case "VERIFIED":
		return "SUPPORTS"
	case "ABSTAIN":
		return "ABSTAINS"
	default:
		return "MENTIONS"
	}
}

// stakeoutList reads the watches from the ragstore's SQLite.
func stakeoutList(ctx context.Context) ([]searchdet.StakeoutJob, error) {
	m, err := ragManagerOf()
	if err != nil {
		return nil, err
	}
	jobs, err := m.StakeoutList(ctx)
	if err != nil {
		return nil, err
	}
	// Rows written before the watch gate existed are masked on the way out,
	// so the page never shows what an investigate call would not.
	for i := range jobs {
		jobs[i] = searchdet.SanitizeWatch(jobs[i])
	}
	return jobs, nil
}

// stakeoutCheck runs one watch for real and stores the new snapshot.
func stakeoutCheck(ctx context.Context, id string) (searchdet.StakeoutDiff, searchdet.StakeoutJob, error) {
	m, err := ragManagerOf()
	if err != nil {
		return searchdet.StakeoutDiff{}, searchdet.StakeoutJob{}, err
	}
	job, ok, err := m.StakeoutGet(ctx, id)
	if err != nil {
		return searchdet.StakeoutDiff{}, searchdet.StakeoutJob{}, err
	}
	if !ok {
		return searchdet.StakeoutDiff{}, searchdet.StakeoutJob{}, fmt.Errorf("no stakeout job %q", id)
	}
	diff, updated, err := searchdet.CheckStakeout(ctx, job, searchdet.ResolveKeys())
	if err != nil {
		return diff, updated, err
	}
	if err := m.StakeoutSave(ctx, updated); err != nil {
		return diff, updated, err
	}
	// The stored query is the masked one the next check will send; the page
	// shows the same policy as every other result field.
	return diff, searchdet.SanitizeWatch(updated), nil
}

// stakeoutCheckAll checks every saved watch, one by one.
func stakeoutCheckAll(ctx context.Context) ([]searchdet.StakeoutJob, []searchdet.StakeoutDiff, error) {
	m, err := ragManagerOf()
	if err != nil {
		return nil, nil, err
	}
	jobs, err := m.StakeoutList(ctx)
	if err != nil {
		return nil, nil, err
	}
	updated := make([]searchdet.StakeoutJob, 0, len(jobs))
	diffs := make([]searchdet.StakeoutDiff, 0, len(jobs))
	for _, j := range jobs {
		d, job, err := searchdet.CheckStakeout(ctx, j, searchdet.ResolveKeys())
		if err != nil {
			job = j
			job.StatusBadge = "⚠️ 巡检失败"
			job.LatestDiff = err.Error()
		} else {
			_ = m.StakeoutSave(ctx, job)
		}
		updated = append(updated, searchdet.SanitizeWatch(job))
		diffs = append(diffs, d)
	}
	return updated, diffs, nil
}

// molaFirstRunes cuts a title down for a default job name.
func molaFirstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// stripDataURL removes a data:…;base64, prefix.
func stripDataURL(s string) string {
	if i := strings.Index(s, ","); i >= 0 && strings.HasPrefix(s, "data:") {
		return s[i+1:]
	}
	return s
}

// injectAgent writes the Detective skill into the agent's own skills folder
// and its MCP entry into the agent's own config, or takes both out. What it
// wrote is returned, so the page shows real paths.
func injectAgent(agentID string, on bool) (map[string]any, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	written := []string{}
	removed := []string{}
	var matched []string
	for _, t := range library.Targets() {
		if t.Agent == nil || t.Gone {
			continue
		}
		id := t.Agent.ID
		if !agentMatches(id, agentID) {
			continue
		}
		matched = append(matched, id)
		if t.Skills != "" {
			dir := filepath.Join(t.Skills, "molasearch-detective")
			file := filepath.Join(dir, "SKILL.md")
			if on {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return nil, err
				}
				if err := writeFileAtomic(file, []byte(detectiveSkill)); err != nil {
					return nil, err
				}
				written = append(written, file)
			} else if err := os.RemoveAll(dir); err == nil {
				removed = append(removed, file)
			}
		}
		if t.MCP != nil && t.MCP.Path != "" {
			path := t.MCP.Path
			if on {
				if err := writeMCPEntry(path, "molasearch", map[string]any{
					"command": exe,
					"args":    []string{"mcp", "search"},
				}); err != nil {
					return nil, err
				}
				written = append(written, path)
			} else {
				if err := removeMCPEntry(path, "molasearch"); err == nil {
					removed = append(removed, path)
				}
			}
		}
	}
	if len(matched) == 0 {
		return nil, fmt.Errorf("no agent %q is installed here", agentID)
	}
	return map[string]any{
		"agent": agentID, "enabled": on, "matched": matched,
		"written": written, "removed": removed,
	}, nil
}

// agentMatches maps the page's chip ids onto magpie's agent ids.
func agentMatches(id, chip string) bool {
	id = strings.ToLower(id)
	chip = strings.ToLower(chip)
	if id == chip {
		return true
	}
	aliases := map[string][]string{
		"claude":      {"claude", "claudecode", "claude-code", "cc"},
		"dsh":         {"dsh"},
		"codex":       {"codex"},
		"gemini":      {"gemini", "geminicli"},
		"opencode":    {"opencode", "oc"},
		"cursor":      {"cursor"},
		"antigravity": {"antigravity", "antigravity-cli"},
	}
	for _, a := range aliases[chip] {
		if id == a || strings.Contains(id, a) {
			return true
		}
	}
	return false
}

// writeFileAtomic writes through a temp file and renames, so a reader never
// sees half a file.
func writeFileAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mola-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// writeMCPEntry merges one server into a JSON MCP file (and its siblings),
// keeping every other server the user has.
func writeMCPEntry(path, name string, entry map[string]any) error {
	b, err := os.ReadFile(path)
	var root map[string]any
	if err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &root); err != nil {
			return fmt.Errorf("%s is not JSON (%w); not touching it", path, err)
		}
	}
	if root == nil {
		root = map[string]any{}
	}
	key := "mcpServers"
	if strings.Contains(path, "opencode") {
		key = "mcp"
	}
	servers, _ := root[key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[name] = entry
	root[key] = servers
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, out)
}

// removeMCPEntry takes magpie's server out of a JSON MCP file.
func removeMCPEntry(path, name string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return err
	}
	for _, key := range []string{"mcpServers", "mcp"} {
		if servers, ok := root[key].(map[string]any); ok {
			delete(servers, name)
		}
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, out)
}

// detectiveSkill is the SKILL.md the injection writes: it tells an agent
// when to call the gateway and what the modes mean.
const detectiveSkill = `---
name: molasearch-detective
description: "MolaSearch 侦探搜索网关：真实多源联网检索、30 种搜索模式、辟谣核验、时间树、多模态取证与五层记忆。需要查证、找一手信源或核实网传说法时使用。"
---

# 🔍 MolaSearch 侦探搜索网关

## 核心原则
「少点幻觉，多点搜索；查得到，信得过；查不到敢说不知道。」

调用本网关的 MCP 工具时：
1. 先用 ` + "`magpie_investigate`" + ` 做一次完整查证（默认 M11 模式，开启 Self-RAG 拒答门控）；
2. 需要快速事实时用 ` + "`magpie_search_fast`" + `（M01）；
3. 需要正反双方证据时用 ` + "`magpie_debate`" + `（M13）；
4. 需要时间演化时用 ` + "`magpie_timeline`" + `（M17）；
5. 结果中的 ` + "`verdict`" + ` 为 ` + "`ABSTAIN`" + ` 时，**必须如实告诉用户查不到**，不得凭记忆补答。

## 30 种模式（M01~M30）
- 增强族 M01~M04：直通、扩展召回、稠密+稀疏、多查询扩展
- 图谱族 M05~M08：实体多跳、LightRAG 共现图、引文雪球
- 递归族 M09~M12：AREX 缺口迭代、RAAC 新颖度收敛、Self-RAG 拒答、递归分解
- 辩论族 M13~M16：正反方对抗、多轮辩论、发散探索
- 时间族 M17~M20：时间树、强时效、垂域、权威加权
- 隐私族 M21~M24：脱敏、零留存
- 多模态族 M25~M30：气象核验、图片取证、视频弹幕、DOM 无障碍树、ADB、模型对照

## 输出
- ` + "`verdict`" + `: VERIFIED / REFUTED / UNVERIFIED / ABSTAIN
- ` + "`citations`" + `: 每条都带真实 URL、域名、权威等级 0~5
- ` + "`agentEnvelopeXml`" + `: 给 Agent 的紧凑证据信封（< 800 tokens）
`
