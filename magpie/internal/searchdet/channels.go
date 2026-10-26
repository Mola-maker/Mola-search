// The channel framework: every source the engine can ask is a Channel; a
// run asks the enabled ones at once, merges what returns, scores it by
// domain authority and measures each one for the trajectory.
package searchdet

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Channel is one live source the engine can ask.
type Channel interface {
	ID() string
	Name() string
	Enabled(k Keys) bool
	Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error)
}

// channels is every channel in priority order: keyed ones first (they answer
// cleanest), then the keyless aggregators that need no configuration.
func channels() []Channel {
	return []Channel{
		&tencentChannel{},
		&tavilyChannel{},
		&exaChannel{},
		&braveChannel{},
		&searxngChannel{},
		&youFreeChannel{},
		&ddgChannel{},
		&bingChannel{},
		&wikipediaChannel{},
		&baikeChannel{},
		&githubChannel{},
		&bilibiliChannel{},
		&arxivChannel{},
	}
}

// channelByID finds a channel by id.
func channelByID(id string) Channel {
	for _, c := range channels() {
		if c.ID() == id {
			return c
		}
	}
	return nil
}

// runResult is one channel's citations and its measured run.
type runResult struct {
	run  channelRun
	cite []Citation
}

// runChannels asks every enabled (or selected) channel concurrently, with a
// per-channel deadline, and returns each result as it stands: citations
// found, latency measured, error kept. A channel that fails never fails the
// run. timeout <= 0 keeps the engine's default deadline.
func runChannels(ctx context.Context, query string, k Keys, n int, only map[string]bool, timeout time.Duration) []runResult {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		out  []runResult
		t0   = time.Now()
		seen = map[string]bool{}
	)
	for _, c := range channels() {
		c := c
		if only != nil && !only[c.ID()] {
			continue
		}
		if !c.Enabled(k) {
			continue
		}
		if seen[c.ID()] {
			continue
		}
		seen[c.ID()] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := channelDeadline(ctx, timeout)
			defer cancel()
			start := time.Since(t0)
			at := time.Now()
			cites, err := c.Search(cctx, query, k, n)
			took := time.Since(t0) - start
			rr := runResult{
				run: channelRun{
					Name:      c.Name(),
					ChannelID: c.ID(),
					Query:     query,
					Latency:   took,
					At:        at,
					RawHits:   len(cites),
					Err:       err,
				},
				cite: cites,
			}
			for i := range rr.cite {
				if rr.cite[i].Channel == "" {
					rr.cite[i].Channel = c.ID()
				}
				if rr.cite[i].SourceName == "" {
					rr.cite[i].SourceName = c.Name()
				}
			}
			mu.Lock()
			out = append(out, rr)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].run.RawHits != out[j].run.RawHits {
			return out[i].run.RawHits > out[j].run.RawHits
		}
		return out[i].run.ChannelID < out[j].run.ChannelID
	})
	return out
}

// mergeCitations deduplicates by URL across channels (the earlier channel
// wins), fills domain and authority, and caps the list.
func mergeCitations(all []Citation, cap int) []Citation {
	seen := map[string]bool{}
	var out []Citation
	for _, c := range all {
		key := normalizeURL(c.URL)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		c.Domain = domainOf(c.URL)
		if c.AuthorityLevel == 0 {
			c.AuthorityLevel = authorityOf(c.Domain)
		}
		c.Title = strings.TrimSpace(c.Title)
		c.Snippet = strings.TrimSpace(c.Snippet)
		if c.Title == "" {
			c.Title = c.Domain
		}
		out = append(out, c)
		if len(out) >= cap {
			break
		}
	}
	return out
}

// NormalizeURL is normalizeURL for a caller that must dedupe by page the
// way the engine does (the route registry's conflict resolution).
func NormalizeURL(raw string) string { return normalizeURL(raw) }

// DomainOf is domainOf for a caller that groups evidence by publisher.
func DomainOf(raw string) string { return domainOf(raw) }

// normalizeURL strips fragments and tracking params so the same page from
// two channels deduplicates.
func normalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.Fragment = ""
	q := u.Query()
	for _, k := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "from", "spm_id_from", "vd_source", "share_source", "refer_flag"} {
		q.Del(k)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// errNoResults is a channel answering with nothing — reported, not fatal.
var errNoResults = errors.New("no results")

// ErrChannelNotConfigured says a channel's own keys are not present, so a
// probe cannot ask it.
var ErrChannelNotConfigured = errors.New("channel has no key configured")

// ProbeChannel asks one channel a short query, for the provider platform's
// connectivity check, and reports how many citations came back. It is a real
// call with the channel's real keys, nothing simulated.
func ProbeChannel(ctx context.Context, id, query string) (int, error) {
	c := channelByID(id)
	if c == nil {
		return 0, fmt.Errorf("unknown channel %q", id)
	}
	k := ResolveKeys()
	if !c.Enabled(k) {
		return 0, ErrChannelNotConfigured
	}
	cites, err := c.Search(ctx, query, k, 3)
	if err != nil {
		return len(cites), err
	}
	if len(cites) == 0 {
		return 0, errNoResults
	}
	return len(cites), nil
}

// cite builds a citation quickly.
func cite(title, u, snippet, channel, published string) Citation {
	return Citation{Title: title, URL: u, Snippet: snippet, Channel: channel, PublishedAt: published}
}

// rankConfig is the part of the settings that changes ranking.
type rankConfig struct {
	AuthBoost bool // weight authoritative domains up
	FreshOnly bool // weight recent publication up
}

// rankCitations orders merged citations by a real relevance score: coverage
// of the query's tokens in title and snippet, the domain's authority, and
// freshness. The weights change with the mode's plan, and every weight is
// still multiplied by a score computed here.
func rankCitations(cites []Citation, query string, now time.Time, cfg rankConfig) []Citation {
	qt := map[string]int{}
	for _, t := range tokenize(query) {
		qt[t]++
	}
	type sc struct {
		c    Citation
		rank float64
	}
	out := make([]sc, 0, len(cites))
	for _, c := range cites {
		titleToks := tokenize(c.Title)
		snipToks := tokenize(c.Snippet)
		th := map[string]bool{}
		for _, t := range titleToks {
			th[t] = true
		}
		sh := map[string]bool{}
		for _, t := range snipToks {
			sh[t] = true
		}
		cover := 0.0
		for t := range qt {
			w := 1.0
			if th[t] {
				w = 2.0
			}
			if sh[t] {
				w += 1.0
			}
			if w > 1 {
				cover += w
			}
		}
		maxCover := float64(len(qt)) * 3
		if maxCover == 0 {
			maxCover = 1
		}
		rel := cover / maxCover
		auth := float64(c.AuthorityLevel) / 5
		fresh := 1.0
		if age := ageDays(c.PublishedAt, now); age > 0 {
			fresh = 1.0 / (1.0 + age/365)
		}
		wRel, wAuth, wFresh := 0.5, 0.35, 0.15
		if cfg.AuthBoost {
			wRel, wAuth, wFresh = 0.35, 0.5, 0.15
		}
		if cfg.FreshOnly {
			wRel, wAuth, wFresh = 0.35, 0.25, 0.4
		}
		rank := rel*wRel + auth*wAuth + fresh*wFresh
		out = append(out, sc{c, rank})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].rank > out[j].rank })
	cites2 := make([]Citation, 0, len(out))
	for _, s := range out {
		cites2 = append(cites2, s.c)
	}
	return cites2
}

// fmtMs renders a duration for the trajectory panel.
func fmtMs(d time.Duration) string { return fmt.Sprintf("%.0f ms", float64(d.Microseconds())/1000) }
