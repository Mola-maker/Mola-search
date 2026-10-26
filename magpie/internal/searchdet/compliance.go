// The compliance gate in front of every investigation: personal data is
// masked, the red lines are refused, and a policy that cannot be read stops
// the search instead of letting it through. The built-in list is the
// default; MOLA_COMPLIANCE_FILE replaces it with one term per line. A
// configured file that is missing, unreadable or empty fails closed.
package searchdet

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/yetone/magpie/internal/redact"
)

// ComplianceFileEnv names the optional red-line file. Setting it makes the
// file the whole policy: one term per line, blank lines and # comments
// ignored. An empty or unreadable file blocks every investigation.
const ComplianceFileEnv = "MOLA_COMPLIANCE_FILE"

// defaultRedlineTerms is the policy used when no file is configured. These
// are the same six rules the product has always carried; the file exists so
// they can be audited and extended without a rebuild.
var defaultRedlineTerms = []string{
	"选举内幕",
	"地缘政变",
	"人肉开盒",
	"开房记录",
	"查某人家庭住址",
	"私生活",
}

var reIPv4 = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`)

// Redact is the privacy pass with the built-in policy. The engine calls
// redactForInvestigation, which also enforces a configured policy file.
func Redact(query string) RedactAudit { return redactWithTerms(query, defaultRedlineTerms) }

// redactForInvestigation is the gate the engine runs. A policy it cannot
// read is a block, not a pass: the search does not leave the machine with
// an unknown red-line list.
func redactForInvestigation(query string) RedactAudit {
	audit, _ := redactForInvestigationPolicy(query)
	return audit
}

// redactForInvestigationPolicy is redactForInvestigation and the terms it
// used: the output guard needs the same list to drop results that carry a
// red-line term back.
func redactForInvestigationPolicy(query string) (RedactAudit, []string) {
	terms, err := loadRedlineTerms()
	if err != nil {
		return blockedRedact(query, "合规词表读取失败，按 fail-closed 拒绝检索："+err.Error()), nil
	}
	return redactWithTerms(query, terms), terms
}

func blockedRedact(query, reason string) RedactAudit {
	return RedactAudit{
		MaskedQuery:      query,
		Placeholders:     []string{},
		ComplianceStatus: "BLOCKED_REDLINE",
		BlockedReason:    reason,
	}
}

func redactWithTerms(query string, terms []string) RedactAudit {
	audit := RedactAudit{
		MaskedQuery:      query,
		Placeholders:     []string{},
		ComplianceStatus: "PASS (已通过时政与个人隐私红线校验)",
	}
	if term := redlineHit(query, terms); term != "" {
		audit.ComplianceStatus = "BLOCKED_REDLINE"
		audit.BlockedReason = "触发合规红线（" + term + "）：不碰时政与涉及个人隐私开盒的消息。"
		return audit
	}
	q := query
	var hits []redact.Hit
	q, _ = redact.Mask(q, redact.Options{
		Secrets:  true,
		Personal: true,
		Seen:     func(h redact.Hit) { hits = append(hits, h) },
	})
	for _, h := range hits {
		audit.MaskedCount++
		audit.Placeholders = append(audit.Placeholders, h.Placeholder)
	}
	// magpie's rules cover keys, tokens, passwords, ID cards, bank cards,
	// phones and email; an IPv4 address is not personal data to them, so the
	// engine's own mask keeps it here.
	q = reIPv4.ReplaceAllStringFunc(q, func(string) string {
		audit.MaskedCount++
		audit.Placeholders = append(audit.Placeholders, "[IPV4]")
		return "[IPV4]"
	})
	audit.MaskedQuery = q
	return audit
}

// redlineHit reports the first term the query carries. Matching ignores
// case, spaces and punctuation inside both sides, so "人 肉·开 盒" is the
// same request as "人肉开盒". A keyword list is a floor, not a proof of
// intent; the file is the auditable part.
func redlineHit(query string, terms []string) string {
	norm := normalizeForMatch(query)
	if norm == "" {
		return ""
	}
	for _, term := range terms {
		t := normalizeForMatch(term)
		if t != "" && strings.Contains(norm, t) {
			return term
		}
	}
	return ""
}

func normalizeForMatch(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// bomUTF8 is a UTF-8 byte order mark, which an editor may leave at the
// start of the policy file. It is written as bytes so no source escape can
// be confused with a real mark.
const bomUTF8 = "\xEF\xBB\xBF"

// loadRedlineTerms reads the configured policy, or returns the built-in
// list when none is configured. A file that cannot be read, cannot be
// scanned or holds no term is an error: the caller blocks.
func loadRedlineTerms() ([]string, error) {
	path := strings.TrimSpace(os.Getenv(ComplianceFileEnv))
	if path == "" {
		return defaultRedlineTerms, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var terms []string
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), bomUTF8))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		terms = append(terms, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(terms) == 0 {
		return nil, fmt.Errorf("%s has no terms", path)
	}
	return terms, nil
}
