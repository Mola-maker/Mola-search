// Text plumbing under the engine: dates out of pages, pages into text,
// domains into authority levels, text into entities — the pieces every
// channel and mode builds on.
package searchdet

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"2006/01/02",
	"2006.01.02",
	"2006年01月02日",
	"2006年1月2日",
	time.RFC1123,
	time.RFC1123Z,
	"02 Jan 2006",
	"Jan 2, 2006",
	"January 2, 2006",
}

// parseDate reads a date a page or API gave in any of the common shapes.
func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

var reDateInText = regexp.MustCompile(`(20\d{2})[-年/.\s](\d{1,2})[-月/.\s](\d{1,2})[日]?`)

// dateRef is a date found in text with the words around it.
type dateRef struct {
	Time  time.Time
	Stage string
}

// extractDates finds up to n dated mentions in a text, newest first.
func extractDates(text string, n int) []dateRef {
	seen := map[string]bool{}
	var out []dateRef
	for _, m := range reDateInText.FindAllStringSubmatch(text, -1) {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		if y < 2000 || mo < 1 || mo > 12 || d < 1 || d > 31 {
			continue
		}
		t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.Local)
		key := t.Format("2006-01-02")
		if seen[key] {
			continue
		}
		seen[key] = true
		stage := strings.TrimSpace(strings.Trim(m[0], " \t,-"))
		out = append(out, dateRef{Time: t, Stage: stage})
		if len(out) >= n*3 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// htmlToText pulls the readable text out of an HTML page, dropping script,
// style and noscript. Block tags break lines so snippets stay legible.
func htmlToText(doc *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg":
				return
			case "p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "section", "article":
				b.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	s := b.String()
	s = strings.Join(strings.Fields(s), " ")
	// collapse the line breaks we added into single spaces but keep them
	// findable: re-split on nothing — Fields already handled runs; the
	// newlines above only stopped words from fusing across blocks.
	return strings.TrimSpace(s)
}

// parseHTML parses an HTML document (a fragment is tolerated).
func parseHTML(s string) (*html.Node, error) {
	return html.Parse(strings.NewReader(s))
}

// domainOf is a URL's host without a www. prefix.
func domainOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	d := u.Hostname()
	return strings.TrimPrefix(d, "www.")
}

// authorityOf rates a domain 0~5: 5 a government, standards body or the
// platform itself; 4 a major encyclopedia, journal or top news room; 3 a
// known outlet; 1 a content farm; 0 unknown (scored, not blocked).
func authorityOf(domain string) int {
	if domain == "" {
		return 0
	}
	first := strings.Split(domain, ".")
	if len(first) >= 2 {
		// x.gov.cn, x.edu.cn: the second-level decides
		if len(first) >= 3 && (first[len(first)-2] == "gov" || first[len(first)-2] == "edu") {
			return 5
		}
	}
	if v, ok := authorityTable[domain]; ok {
		return v
	}
	// try the last two labels (handles news.site.com.cn)
	if i := strings.Index(domain, "."); i > 0 {
		if v, ok := authorityTable[domain[i+1:]]; ok {
			return v
		}
	}
	return 0
}

var authorityTable = map[string]int{
	// government and standards
	"gov.cn": 5, "cma.gov.cn": 5, "nhc.gov.cn": 5, "samr.gov.cn": 5,
	"stats.gov.cn": 5, "court.gov.cn": 5, "mps.gov.cn": 5, "gov": 5,
	"edu.cn": 5, "edu": 5, "un.org": 5, "nasa.gov": 5,
	"piyao.org.cn": 5, "china.com.cn": 4, "xinhuanet.com": 4, "people.com.cn": 4,
	// encyclopedias and journals
	"wikipedia.org": 4, "britannica.com": 4, "scholar.google.com": 4, "arxiv.org": 4,
	"nature.com": 5, "science.org": 5, "sciencedirect.com": 4, "springer.com": 4,
	"ieeexplore.ieee.org": 4, "pubmed.ncbi.nlm.nih.gov": 5, "ncbi.nlm.nih.gov": 5,
	"who.int": 5,
	// major outlets (CN)
	"cctv.com": 4, "news.cn": 4, "chinanews.com.cn": 4, "thepaper.cn": 4,
	"caixin.com": 4, "bjnews.com.cn": 4, "yicai.com": 4, "jiemian.com": 3,
	"36kr.com": 3, "ifeng.com": 3, "sina.com.cn": 3, "163.com": 3, "sohu.com": 2,
	"qq.com": 3, "baidu.com": 2, "zhihu.com": 2, "douyin.com": 2,
	// major outlets (EN)
	"reuters.com": 4, "apnews.com": 4, "bbc.com": 4, "nytimes.com": 4,
	"theguardian.com": 4, "wsj.com": 4, "bloomberg.com": 4, "ft.com": 4,
	"cnn.com": 3, "washingtonpost.com": 4, "economist.com": 4,
	// developer platforms
	"github.com": 4, "stackoverflow.com": 3, "developer.mozilla.org": 4,
	"mozilla.org": 4, "w3.org": 5, "python.org": 4, "golang.org": 4, "go.dev": 4,
	// video/bilibili (first-party data about itself)
	"bilibili.com": 4, "damai.cn": 4, "maoyan.com": 4,
	// weather/geo first-party
	"open-meteo.com": 4, "amap.com": 4, "loli.net": 1,
	// content farms and SEO sinks
	"baijiahao.baidu.com": 1, "csdn.net": 1, "blog.csdn.net": 1,
	"weibo.com": 2, "toutiao.com": 2, "360doc.com": 1, "docin.com": 1,
}

// entitiesIn pulls likely named entities out of text: CJK runs of 2~6
// characters that repeat, and Latin capitalized words, most frequent first.
func entitiesIn(text string, n int) []string {
	freq := map[string]int{}
	reCJK := regexp.MustCompile(`[\p{Han}]{2,6}`)
	for _, m := range reCJK.FindAllString(text, -1) {
		freq[m]++
	}
	reCap := regexp.MustCompile(`\b[A-Z][a-zA-Z0-9]{2,}\b`)
	for _, m := range reCap.FindAllString(text, -1) {
		freq[m]++
	}
	stop := map[string]bool{"来自": true, "搜索": true, "结果": true, "视频": true, "图片": true, "新闻": true, "首页": true, "下载": true, "查看": true, "更多": true, "评论": true}
	type kv struct {
		k string
		v int
	}
	var kvs []kv
	for k, v := range freq {
		if v >= 2 && !stop[k] && len([]rune(k)) >= 2 {
			kvs = append(kvs, kv{k, v})
		}
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].v != kvs[j].v {
			return kvs[i].v > kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})
	out := make([]string, 0, n)
	for _, kv := range kvs {
		out = append(out, kv.k)
		if len(out) >= n {
			break
		}
	}
	return out
}

// snippetAround cuts ~240 chars of text centred on the densest run of query
// tokens, so a citation's snippet is the part that answers, not the page's
// first boilerplate.
func snippetAround(text, query string) string {
	runes := []rune(text)
	if len(runes) <= 240 {
		return text
	}
	qt := map[string]bool{}
	for _, t := range tokenize(query) {
		qt[t] = true
	}
	best, bestScore := 0, 0
	window := 120
	for i := 0; i+window <= len(runes); i += 24 {
		seg := string(runes[i : i+window])
		sc := 0
		for _, t := range tokenize(seg) {
			if qt[t] {
				sc++
			}
		}
		if sc > bestScore {
			best, bestScore = i, sc
		}
	}
	lo := best - 60
	if lo < 0 {
		lo = 0
	}
	hi := lo + 240
	if hi > len(runes) {
		hi = len(runes)
		lo = hi - 240
		if lo < 0 {
			lo = 0
		}
	}
	return strings.TrimSpace(string(runes[lo:hi]))
}
