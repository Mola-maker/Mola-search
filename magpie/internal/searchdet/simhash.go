// 64-bit SimHash near-duplicate folding with AIHOT time decay: results that
// say the same thing collapse into one, and the older a result is the less
// it counts, so a stale copy never buries a fresh correction.
package searchdet

import (
	"math"
	"math/bits"
	"strings"
	"time"
)

// simhash64 is the SimHash of a text over its tokens: each token hashes to
// a 64-bit value and votes by its weight for the bits it sets.
func simhash64(text string) uint64 {
	toks := tokenize(text)
	if len(toks) == 0 {
		return 0
	}
	freq := map[string]int{}
	for _, t := range toks {
		freq[t]++
	}
	var bitsArr [64]int
	for tok, w := range freq {
		h := fnv1a(tok)
		for i := 0; i < 64; i++ {
			if h&(1<<i) != 0 {
				bitsArr[i] += w
			} else {
				bitsArr[i] -= w
			}
		}
	}
	var v uint64
	for i := 0; i < 64; i++ {
		if bitsArr[i] > 0 {
			v |= 1 << i
		}
	}
	return v
}

func fnv1a(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// hamming64 is how many bits two SimHashes differ in.
func hamming64(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// nearDup is whether two SimHashes are the same writing (<=6 bits apart, the
// usual SimHash cut for near-duplicate text).
func nearDup(a, b uint64) bool { return hamming64(a, b) <= 6 }

// decay folds age into a score: a result a day old counts 35% of a fresh
// one (gamma = 0.35), levelling off so nothing reaches zero.
func decay(score float64, ageDays float64, gamma float64) float64 {
	if ageDays <= 0 {
		return score
	}
	return score * math.Pow(gamma, math.Min(ageDays, 30))
}

// ageDays is how old a published date is, 0 when unknown or unparseable.
func ageDays(publishedAt string, now time.Time) float64 {
	t, ok := parseDate(publishedAt)
	if !ok {
		return 0
	}
	d := now.Sub(t).Hours() / 24
	if d < 0 {
		return 0
	}
	return d
}

// foldResults folds near-duplicate citations (SimHash over title+snippet)
// keeping the strongest by decayed score, and returns how many were folded.
func foldResults(cites []Citation, now time.Time, gamma float64) (kept []Citation, folded int) {
	type entry struct {
		cite  Citation
		hash  uint64
		score float64
	}
	entries := make([]entry, 0, len(cites))
	for _, c := range cites {
		entries = append(entries, entry{
			cite:  c,
			hash:  simhash64(c.Title + " " + c.Snippet),
			score: decay(authorityWeight(c)+1, ageDays(c.PublishedAt, now), gamma),
		})
	}
	var out []entry
	for _, e := range entries {
		merged := false
		for i := range out {
			if nearDup(out[i].hash, e.hash) {
				if e.score > out[i].score {
					out[i] = e
				}
				merged = true
				folded++
				break
			}
		}
		if !merged {
			out = append(out, e)
		}
	}
	for _, e := range out {
		kept = append(kept, e.cite)
	}
	return kept, folded
}

// authorityWeight is a citation's standing from its domain, 0~5.
func authorityWeight(c Citation) float64 { return float64(c.AuthorityLevel) }

// tokenize splits text into indexable tokens: CJK runs as bigrams, Latin
// and digit runs as lower-cased words.
func tokenize(text string) []string {
	var toks []string
	var cjk, word []rune
	flushCJK := func() {
		if len(cjk) >= 2 {
			for i := 0; i+2 <= len(cjk); i++ {
				toks = append(toks, string(cjk[i:i+2]))
			}
		} else if len(cjk) == 1 {
			toks = append(toks, string(cjk))
		}
		cjk = cjk[:0]
	}
	flushWord := func() {
		if len(word) > 0 {
			toks = append(toks, string(word))
			word = word[:0]
		}
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 0x4e00 && r <= 0x9fff, r >= 0x3400 && r <= 0x4dbf:
			flushWord()
			cjk = append(cjk, r)
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			flushCJK()
			word = append(word, r)
		default:
			flushCJK()
			flushWord()
		}
	}
	flushCJK()
	flushWord()
	return toks
}
