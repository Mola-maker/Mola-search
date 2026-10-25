// The keyed channels: the search APIs the user can set up with a key, read
// from env/.env or magpie's own providers.json. The same wire formats the
// gateway's own search serves.
package searchdet

import (
	"context"
	"net/url"
)

// ---------------------------------------------------------------------------
// Tavily
// ---------------------------------------------------------------------------

type tavilyChannel struct{}

func (tavilyChannel) ID() string          { return "tavily" }
func (tavilyChannel) Name() string        { return "Tavily" }
func (tavilyChannel) Enabled(k Keys) bool { return k.Tavily != "" }

func (tavilyChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	var out struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Date    string `json:"publishedDate"`
		} `json:"results"`
	}
	err := postJSON(ctx, "https://api.tavily.com/search",
		map[string]string{"Authorization": "Bearer " + k.Tavily},
		map[string]any{"query": query, "max_results": n, "search_depth": "basic"}, &out)
	if err != nil {
		return nil, err
	}
	var cites []Citation
	for _, r := range out.Results {
		if r.URL == "" {
			continue
		}
		cites = append(cites, cite(r.Title, r.URL, r.Content, "tavily", r.Date))
	}
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return cites, nil
}

// ---------------------------------------------------------------------------
// Exa
// ---------------------------------------------------------------------------

type exaChannel struct{}

func (exaChannel) ID() string          { return "exa" }
func (exaChannel) Name() string        { return "Exa" }
func (exaChannel) Enabled(k Keys) bool { return k.Exa != "" }

func (exaChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	var out struct {
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Text  string `json:"text"`
			Date  string `json:"publishedDate"`
		} `json:"results"`
	}
	err := postJSON(ctx, "https://api.exa.ai/search",
		map[string]string{"X-API-Key": k.Exa},
		map[string]any{"query": query, "numResults": n,
			"contents": map[string]any{"text": map[string]any{"maxCharacters": 500}}}, &out)
	if err != nil {
		return nil, err
	}
	var cites []Citation
	for _, r := range out.Results {
		if r.URL == "" {
			continue
		}
		cites = append(cites, cite(r.Title, r.URL, r.Text, "exa", r.Date))
	}
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return cites, nil
}

// ---------------------------------------------------------------------------
// Brave Search
// ---------------------------------------------------------------------------

type braveChannel struct{}

func (braveChannel) ID() string          { return "brave" }
func (braveChannel) Name() string        { return "Brave Search" }
func (braveChannel) Enabled(k Keys) bool { return k.Brave != "" }

func (braveChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	var out struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Age         string `json:"age"`
			} `json:"results"`
		} `json:"web"`
	}
	err := fetchJSON(ctx, "https://api.search.brave.com/res/v1/web/search?q="+url.QueryEscape(query)+"&count="+itoa(n),
		map[string]string{"X-Subscription-Token": k.Brave, "Accept": "application/json"}, &out)
	if err != nil {
		return nil, err
	}
	var cites []Citation
	for _, r := range out.Web.Results {
		if r.URL == "" {
			continue
		}
		cites = append(cites, cite(r.Title, r.URL, r.Description, "brave", r.Age))
	}
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return cites, nil
}

// ---------------------------------------------------------------------------
// SearXNG (the user's own instance)
// ---------------------------------------------------------------------------

type searxngChannel struct{}

func (searxngChannel) ID() string          { return "searxng" }
func (searxngChannel) Name() string        { return "SearXNG" }
func (searxngChannel) Enabled(k Keys) bool { return k.SearXNGURL != "" }

func (searxngChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	var out struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			PubDate int64  `json:"publishedDate"`
		} `json:"results"`
	}
	base := k.SearXNGURL
	err := fetchJSON(ctx, base+"/search?q="+url.QueryEscape(query)+"&format=json&language=zh-CN", nil, &out)
	if err != nil {
		return nil, err
	}
	var cites []Citation
	for _, r := range out.Results {
		if r.URL == "" {
			continue
		}
		pub := ""
		if r.PubDate > 0 {
			pub = unixDate(r.PubDate)
		}
		cites = append(cites, cite(r.Title, r.URL, r.Content, "searxng", pub))
	}
	if len(cites) == 0 {
		return nil, errNoResults
	}
	return capCites(cites, n), nil
}
