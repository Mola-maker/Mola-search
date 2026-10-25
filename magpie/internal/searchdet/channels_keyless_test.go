package searchdet

import (
	"os"
	"strings"
	"testing"
)

// TestBilibiliCitesFromDocReadsCardTitles covers the shapes a real
// search.bilibili.com/all page has: each video is two anchors with the same
// href (the cover first, whose text is the view count and duration), the
// name is an h3's title attribute, the href is protocol-relative, and a
// cover-only card has no name at all.
func TestBilibiliCitesFromDocReadsCardTitles(t *testing.T) {
	body, err := os.ReadFile("testdata/bilibili_search.html")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseHTML(string(body))
	if err != nil {
		t.Fatal(err)
	}
	cites := bilibiliCitesFromDoc(doc, 10)
	if len(cites) != 2 {
		t.Fatalf("got %d citations, want 2 (cover-only card has no title): %+v", len(cites), cites)
	}
	first := cites[0]
	if first.Title != "刚刚，Mythos 5破解量子计算重大难题！53小时碾压人类数年" {
		t.Fatalf("first title = %q", first.Title)
	}
	if first.URL != "https://www.bilibili.com/zh-Hans/video/BV1jfJY66E6S/" {
		t.Fatalf("first url = %q", first.URL)
	}
	if cites[1].Title != "量子计算 & Quantum Computing 2026学期" {
		t.Fatalf("second title = %q (entity not unescaped?)", cites[1].Title)
	}
	if cites[1].URL != "https://www.bilibili.com/zh-Hans/video/BV1BTPozUExo/" {
		t.Fatalf("second url = %q", cites[1].URL)
	}
	for _, c := range cites {
		if strings.Contains(c.Title, "03:00") || strings.Contains(c.Title, "1897") {
			t.Fatalf("view count or duration leaked into a title: %q", c.Title)
		}
		if !strings.HasPrefix(c.URL, "https://") {
			t.Fatalf("url is not absolute: %q", c.URL)
		}
		if c.Channel != "bilibili" {
			t.Fatalf("channel = %q", c.Channel)
		}
	}
}

func TestBilibiliCitesFromDocCaps(t *testing.T) {
	body, err := os.ReadFile("testdata/bilibili_search.html")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseHTML(string(body))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(bilibiliCitesFromDoc(doc, 1)); got != 1 {
		t.Fatalf("cap 1 kept %d", got)
	}
}

func TestBilibiliAbsURL(t *testing.T) {
	cases := map[string]string{
		"//www.bilibili.com/zh-Hans/video/BV1jfJY66E6S/": "https://www.bilibili.com/zh-Hans/video/BV1jfJY66E6S/",
		"https://www.bilibili.com/video/BV1jfJY66E6S/":   "https://www.bilibili.com/video/BV1jfJY66E6S/",
		"/video/BV1jfJY66E6S/":                           "https://www.bilibili.com/video/BV1jfJY66E6S/",
	}
	for in, want := range cases {
		if got := bilibiliAbsURL(in); got != want {
			t.Fatalf("bilibiliAbsURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStripTagsUnescapes is the API side: Wikipedia's snippets and Bilibili's
// titles arrive entity-escaped, and a title read without unescaping shows
// "&#39;" or "&amp;" to the reader.
func TestStripTagsUnescapes(t *testing.T) {
	got := stripTags(`<span class="searchmatch">北京</span> &quot;冰雹&quot; &#39;预警&#39; &amp; hail`)
	want := `北京 "冰雹" '预警' & hail`
	if got != want {
		t.Fatalf("stripTags = %q, want %q", got, want)
	}
}
