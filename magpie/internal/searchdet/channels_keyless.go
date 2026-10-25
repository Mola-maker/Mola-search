// The keyless channels: sources the engine can ask with no key at all.
// Each one is best-effort — a changed page or a blocked request yields
// zero results, never a failed run.
package searchdet

import (
	"context"
	"encoding/xml"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// DuckDuckGo HTML (html.duckduckgo.com/html) — no key, stable-ish markup.
// ---------------------------------------------------------------------------

type ddgChannel struct{}

func (ddgChannel) ID() string        { return "ddg" }
func (ddgChannel) Name() string      { return "DuckDuckGo" }
func (ddgChannel) Enabled(Keys) bool { return true }

func (ddgChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	body, err := fetchGet(ctx, "https://html.duckduckgo.com/html/?q="+url.QueryEscape(query), "text/html")
	if err != nil {
		return nil, err
	}
	doc, err := parseHTML(body)
	if err != nil {
		return nil, err
	}
	var cites []Citation
	var walk func(node *htmlNode)
	walk = func(n *htmlNode) {
		if n.Type == elementNode && n.Data == "a" && hasClass(n, "result__a") {
			href := attrOf(n, "href")
			title := nodeText(n)
			if u := ddgUnwrap(href); u != "" && title != "" {
				cites = append(cites, cite(title, u, "", "ddg", ""))
			}
		}
		if n.Type == elementNode && (hasClass(n, "result__snippet") || hasClass(n, "result-snippet")) {
			s := nodeText(n)
			if len(cites) > 0 && cites[len(cites)-1].Snippet == "" {
				cites[len(cites)-1].Snippet = s
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return capCites(cites, n), nil
}

// ddgUnwrap decodes DuckDuckGo's /l/?uddg=<encoded> redirect links.
func ddgUnwrap(href string) string {
	if strings.HasPrefix(href, "//duckduckgo.com/l/") || strings.HasPrefix(href, "/l/") {
		if u, err := url.Parse(href); err == nil {
			if raw := u.Query().Get("uddg"); raw != "" {
				return raw
			}
		}
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	if strings.HasPrefix(href, "/") {
		return ""
	}
	return href
}

// ---------------------------------------------------------------------------
// Bing CN — no key; parsed out of the result list markup.
// ---------------------------------------------------------------------------

type bingChannel struct{}

func (bingChannel) ID() string        { return "bing" }
func (bingChannel) Name() string      { return "Bing" }
func (bingChannel) Enabled(Keys) bool { return true }

func (bingChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	return bingSiteSearch(ctx, query, n, "")
}

// bingSiteSearch asks Bing, optionally scoped to one site (used by the
// Baike channel and authority-targeted modes).
func bingSiteSearch(ctx context.Context, query string, n int, site string) ([]Citation, error) {
	q := query
	if site != "" {
		q = "site:" + site + " " + query
	}
	body, err := fetchGet(ctx, "https://cn.bing.com/search?q="+url.QueryEscape(q)+"&count=15&setlang=zh-hans", "text/html")
	if err != nil {
		return nil, err
	}
	doc, err := parseHTML(body)
	if err != nil {
		return nil, err
	}
	var cites []Citation
	insideAlgo := 0
	var walk func(n *htmlNode)
	walk = func(n *htmlNode) {
		if n.Type == elementNode && n.Data == "li" && hasClass(n, "b_algo") {
			insideAlgo++
		}
		if n.Type == elementNode && n.Data == "h2" && insideAlgo > 0 {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == elementNode && c.Data == "a" {
					href := attrOf(c, "href")
					title := nodeText(c)
					if strings.HasPrefix(href, "http") && title != "" {
						cites = append(cites, cite(title, href, "", "bing", ""))
					}
				}
			}
		}
		if n.Type == elementNode && n.Data == "p" && insideAlgo > 0 && len(cites) > 0 {
			s := nodeText(n)
			if s != "" && cites[len(cites)-1].Snippet == "" && len([]rune(s)) > 20 {
				cites[len(cites)-1].Snippet = s
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return capCites(cites, n), nil
}

// ---------------------------------------------------------------------------
// Wikipedia (zh + en REST search API) — no key.
// ---------------------------------------------------------------------------

type wikipediaChannel struct{}

func (wikipediaChannel) ID() string        { return "wikipedia" }
func (wikipediaChannel) Name() string      { return "Wikipedia" }
func (wikipediaChannel) Enabled(Keys) bool { return true }

func (wikipediaChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	var cites []Citation
	for _, lang := range []string{"zh", "en"} {
		api := "https://" + lang + ".wikipedia.org/w/api.php?action=query&list=search&srlimit=" +
			itoa(n) + "&format=json&srsearch=" + url.QueryEscape(query)
		var out struct {
			Query struct {
				Search []struct {
					Title   string `json:"title"`
					Snippet string `json:"snippet"`
				} `json:"search"`
			} `json:"query"`
		}
		if err := fetchJSON(ctx, api, nil, &out); err != nil {
			continue
		}
		for _, r := range out.Query.Search {
			u := "https://" + lang + ".wikipedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(r.Title, " ", "_"))
			cites = append(cites, cite(r.Title, u, stripTags(r.Snippet), "wikipedia", ""))
		}
	}
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return capCites(cites, n), nil
}

// ---------------------------------------------------------------------------
// 百度百科 via Bing site: search — no key (Baike itself blocks direct
// scraping, so the engine asks Bing for its pages).
// ---------------------------------------------------------------------------

type baikeChannel struct{}

func (baikeChannel) ID() string        { return "baike" }
func (baikeChannel) Name() string      { return "百度百科" }
func (baikeChannel) Enabled(Keys) bool { return true }

func (baikeChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	cites, err := bingSiteSearch(ctx, query, n, "baike.baidu.com")
	if err != nil {
		return nil, err
	}
	for i := range cites {
		cites[i].Channel = "baike"
	}
	return cites, nil
}

// ---------------------------------------------------------------------------
// GitHub repositories API — no key at low rate.
// ---------------------------------------------------------------------------

type githubChannel struct{}

func (githubChannel) ID() string        { return "github" }
func (githubChannel) Name() string      { return "GitHub" }
func (githubChannel) Enabled(Keys) bool { return true }

func (githubChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	api := "https://api.github.com/search/repositories?sort=stars&order=desc&per_page=" + itoa(n) + "&q=" + url.QueryEscape(query)
	var out struct {
		Items []struct {
			FullName    string `json:"full_name"`
			HTMLURL     string `json:"html_url"`
			Description string `json:"description"`
			Stars       int    `json:"stargazers_count"`
			PushedAt    string `json:"pushed_at"`
		} `json:"items"`
	}
	if err := fetchJSON(ctx, api, map[string]string{"Accept": "application/vnd.github+json"}, &out); err != nil {
		return nil, err
	}
	var cites []Citation
	for _, r := range out.Items {
		cites = append(cites, cite(r.FullName, r.HTMLURL, r.Description+" (⭐ "+itoa(r.Stars)+")", "github", r.PushedAt))
	}
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return cites, nil
}

// ---------------------------------------------------------------------------
// Bilibili — video search API first, HTML page as fallback.
// ---------------------------------------------------------------------------

type bilibiliChannel struct{}

func (bilibiliChannel) ID() string        { return "bilibili" }
func (bilibiliChannel) Name() string      { return "哔哩哔哩" }
func (bilibiliChannel) Enabled(Keys) bool { return true }

func (bilibiliChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	if cites := bilibiliAPI(ctx, query, n); len(cites) > 0 {
		return cites, nil
	}
	return bilibiliHTML(ctx, query, n)
}

func bilibiliAPI(ctx context.Context, query string, n int) []Citation {
	api := "https://api.bilibili.com/x/web-interface/search/type?search_type=video&page=1&page_size=" + itoa(n) + "&keyword=" + url.QueryEscape(query)
	var out struct {
		Code int `json:"code"`
		Data struct {
			Result []struct {
				Title       string `json:"title"`
				ARCURL      string `json:"arcurl"`
				Description string `json:"description"`
				Author      string `json:"author"`
				Pubdate     int64  `json:"pubdate"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := fetchJSON(ctx, api, map[string]string{"Referer": "https://www.bilibili.com"}, &out); err != nil || out.Code != 0 {
		return nil
	}
	var cites []Citation
	for _, r := range out.Data.Result {
		if r.ARCURL == "" {
			continue
		}
		pub := ""
		if r.Pubdate > 0 {
			pub = unixDate(r.Pubdate)
		}
		cites = append(cites, cite(stripTags(r.Title), r.ARCURL, r.Author+": "+r.Description, "bilibili", pub))
	}
	return cites
}

func bilibiliHTML(ctx context.Context, query string, n int) ([]Citation, error) {
	body, err := fetchGet(ctx, "https://search.bilibili.com/all?keyword="+url.QueryEscape(query), "text/html")
	if err != nil {
		return nil, err
	}
	doc, err := parseHTML(body)
	if err != nil {
		return nil, err
	}
	cites := bilibiliCitesFromDoc(doc, n)
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return cites, nil
}

// bilibiliCitesFromDoc reads a search page's video cards. Bilibili wraps
// one video in two anchors with the same href: the cover first (whose text
// is the view count and the duration, "1897 03:00"), then the title. Taking
// the first anchor's text named every citation "1897 03:00"; the title is
// in an h3's title attribute, so that is what is read, and the anchor with
// no title of its own is passed over. A later, longer title for the same
// video replaces an earlier one.
func bilibiliCitesFromDoc(doc *htmlNode, n int) []Citation {
	var order []string
	best := map[string]string{}
	var walk func(*htmlNode)
	walk = func(node *htmlNode) {
		if node.Type == elementNode && node.Data == "a" {
			href := attrOf(node, "href")
			if strings.Contains(href, "/video/BV") {
				href = bilibiliAbsURL(href)
				if title := bilibiliAnchorTitle(node); title != "" {
					if _, seen := best[href]; !seen {
						order = append(order, href)
					}
					if len(title) > len(best[href]) {
						best[href] = title
					}
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	var cites []Citation
	for _, href := range order {
		cites = append(cites, cite(best[href], href, "", "bilibili", ""))
	}
	return capCites(cites, n)
}

// bilibiliAbsURL fixes the shapes a search page links in: protocol-relative
// (//www.bilibili.com/…) and root-relative.
func bilibiliAbsURL(href string) string {
	switch {
	case strings.HasPrefix(href, "//"):
		return "https:" + href
	case strings.HasPrefix(href, "http"):
		return href
	default:
		return "https://www.bilibili.com" + href
	}
}

// bilibiliAnchorTitle is the card's own name: the anchor's title, else a
// descendant's (the h3), else a cover image's alt, else "". A cover anchor
// with none of these has only view and duration text, which is not a title.
func bilibiliAnchorTitle(node *htmlNode) string {
	clean := func(s string) string { return strings.TrimSpace(html.UnescapeString(s)) }
	if t := clean(attrOf(node, "title")); t != "" {
		return t
	}
	out := ""
	var walk func(*htmlNode)
	walk = func(n *htmlNode) {
		if out != "" {
			return
		}
		if n.Type == elementNode {
			if t := clean(attrOf(n, "title")); t != "" {
				out = t
				return
			}
			if n.Data == "img" {
				if alt := clean(attrOf(n, "alt")); alt != "" {
					out = alt
					return
				}
			}
		}
		for c := n.FirstChild; c != nil && out == ""; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	return out
}

// ---------------------------------------------------------------------------
// ArXiv Atom API — no key, for research queries.
// ---------------------------------------------------------------------------

type arxivChannel struct{}

func (arxivChannel) ID() string        { return "arxiv" }
func (arxivChannel) Name() string      { return "ArXiv" }
func (arxivChannel) Enabled(Keys) bool { return true }

func (arxivChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	api := "http://export.arxiv.org/api/query?start=0&max_results=" + itoa(n) + "&search_query=all:" + url.QueryEscape(query)
	body, err := fetchGet(ctx, api, "application/atom+xml")
	if err != nil {
		return nil, err
	}
	var feed struct {
		Entries []struct {
			Title     string `xml:"title"`
			ID        string `xml:"id"`
			Summary   string `xml:"summary"`
			Published string `xml:"published"`
			Authors   []struct {
				Name string `xml:"name"`
			} `xml:"author"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal([]byte(body), &feed); err != nil {
		return nil, err
	}
	var cites []Citation
	for _, e := range feed.Entries {
		var names []string
		for _, a := range e.Authors {
			names = append(names, a.Name)
		}
		cites = append(cites, cite(strings.Join(strings.Fields(e.Title), " "), e.ID,
			strings.Join(names, ", ")+": "+firstRunesOf(strings.Join(strings.Fields(e.Summary), " "), 200), "arxiv", e.Published))
	}
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return cites, nil
}

// ---------------------------------------------------------------------------
// You.com free search — no key, rate limited, but real.
// ---------------------------------------------------------------------------

type youFreeChannel struct{}

func (youFreeChannel) ID() string        { return "youfree" }
func (youFreeChannel) Name() string      { return "You.com (free)" }
func (youFreeChannel) Enabled(Keys) bool { return true }

func (youFreeChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	api := "https://api.you.com/v1/agents/search?query=" + url.QueryEscape(query) + "&count=" + itoa(n)
	var out struct {
		Results *struct {
			Web []struct {
				Title       string   `json:"title"`
				URL         string   `json:"url"`
				Description string   `json:"description"`
				Snippets    []string `json:"snippets"`
				PageAge     string   `json:"page_age"`
			} `json:"web"`
		} `json:"results"`
	}
	if err := fetchJSON(ctx, api, nil, &out); err != nil {
		return nil, err
	}
	var cites []Citation
	if out.Results != nil {
		for _, h := range out.Results.Web {
			if h.URL == "" {
				continue
			}
			text := strings.Join(h.Snippets, " ")
			if text == "" {
				text = h.Description
			}
			cites = append(cites, cite(h.Title, h.URL, text, "youfree", h.PageAge))
		}
	}
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return capCites(cites, n), nil
}

// ---------------------------------------------------------------------------
// small shared helpers
// ---------------------------------------------------------------------------

var reTag = regexp.MustCompile(`<[^>]*>`)

// stripTags removes HTML markup and unescapes the entities an API sent —
// Wikipedia's snippets arrive as "&lt;span&gt;…&lt;/span&gt;", and a title
// read without unescaping shows "&#39;" to the reader.
func stripTags(s string) string {
	return strings.TrimSpace(html.UnescapeString(reTag.ReplaceAllString(s, "")))
}

// capCites keeps the first n citations.
func capCites(c []Citation, n int) []Citation {
	if len(c) > n {
		return c[:n]
	}
	return c
}

// itoa is strconv.Itoa by its short name, for API query strings.
func itoa(n int) string { return strconv.Itoa(n) }

// firstRunesOf cuts s to at most n runes.
func firstRunesOf(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// unixDate renders a Unix timestamp as a date.
func unixDate(sec int64) string { return timeFromUnix(sec).Format("2006-01-02") }
