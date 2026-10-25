package redact

// Sanitize replaces every value o covers in s with [KIND] and reports what it
// found. Unlike Mask it remembers nothing: no placeholder is kept for Restore
// and no value is held in memory, so text that leaves the machine (a search
// result shown to the user or handed to an agent) can never be turned back
// into the value it masked. It is the same finders Mask runs — the secret
// rules, the personal-data rules, the user's words and rules, the local model
// — with a fixed marker instead of a placeholder.
func Sanitize(s string, o Options) (string, []Hit) {
	var hits []Hit
	o.Seen = func(h Hit) { hits = append(hits, h) }
	out, _ := mask(s, withModel(o, []string{s}), func(kind, _ string) string {
		return "[" + kind + "]"
	}, false)
	return out, hits
}
