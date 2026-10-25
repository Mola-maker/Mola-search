package searchdet

import "time"

// authFilter is the mode's authority gate: when a run has enough sources
// that are not low quality, the low-quality ones are dropped. It never
// empties the list: if every result is low quality, the caller sees them
// and the verdict says what they are.
func authFilter(cites []Citation, low int) []Citation {
	if len(cites) == 0 {
		return cites
	}
	keep := make([]Citation, 0, len(cites))
	for _, c := range cites {
		if c.AuthorityLevel <= low {
			continue
		}
		keep = append(keep, c)
	}
	if len(keep) == 0 {
		return cites
	}
	return keep
}

// freshFilter is the FreshOnly gate: dated results older than days are
// dropped when at least one result is fresh or undated. An undated result
// stays because "we don't know when" is not "old".
func freshFilter(cites []Citation, now time.Time, days int) []Citation {
	if len(cites) == 0 || days <= 0 {
		return cites
	}
	keep := make([]Citation, 0, len(cites))
	for _, c := range cites {
		age := ageDays(c.PublishedAt, now)
		if age > 0 && age > float64(days) {
			continue
		}
		keep = append(keep, c)
	}
	if len(keep) == 0 {
		return cites
	}
	return keep
}
