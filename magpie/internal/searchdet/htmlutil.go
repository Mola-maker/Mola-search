// Small DOM helpers over x/net/html so the channel parsers say what they
// mean: hasClass(n, "result__a"), attrOf(n, "href"), nodeText(n).
package searchdet

import (
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type htmlNode = html.Node

const elementNode = html.ElementNode

// hasClass says whether n carries class in its class list.
func hasClass(n *htmlNode, class string) bool {
	for _, c := range strings.Fields(attrOf(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

// attrOf is an attribute's value, "" when absent.
func attrOf(n *htmlNode, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// nodeText is the text of n and everything under it, on one line.
func nodeText(n *htmlNode) string {
	var b strings.Builder
	var walk func(*htmlNode)
	walk = func(x *htmlNode) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		if x.Type == html.ElementNode && (x.Data == "script" || x.Data == "style") {
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// timeFromUnix is time.Unix with the local zone, for API timestamps.
func timeFromUnix(sec int64) time.Time { return time.Unix(sec, 0) }

// atoiOr is strconv.Atoi with a fallback.
func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return v
	}
	return def
}

// atof is a float out of a vendor's string field, 0 when it isn't one.
func atof(s string) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return v
	}
	return 0
}
