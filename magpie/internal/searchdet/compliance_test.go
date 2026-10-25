package searchdet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRedactBlocksEveryDefaultTerm(t *testing.T) {
	for _, term := range defaultRedlineTerms {
		a := Redact("帮我查一下" + term + "的资料")
		if a.ComplianceStatus != "BLOCKED_REDLINE" {
			t.Fatalf("term %q was not blocked: %s", term, a.ComplianceStatus)
		}
		if !strings.Contains(a.BlockedReason, term) {
			t.Fatalf("reason does not name the rule %q: %s", term, a.BlockedReason)
		}
		if a.MaskedCount != 0 {
			t.Fatalf("a blocked query must not carry masking work: %+v", a)
		}
	}
}

func TestRedlineIgnoresSpacingAndPunctuation(t *testing.T) {
	for _, q := range []string{"人 肉 开 盒", "人·肉·开·盒", "人肉-开盒", "人 肉，开 盒"} {
		if a := Redact(q); a.ComplianceStatus != "BLOCKED_REDLINE" {
			t.Fatalf("%q slipped through: %s", q, a.ComplianceStatus)
		}
	}
}

func TestComplianceFileIsTheWholePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redlines.txt")
	if err := os.WriteFile(path, []byte(bomUTF8+"# 内部规则\n内部代号\n\n私生活\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ComplianceFileEnv, path)

	a := redactForInvestigation("查一下内部代号的资料")
	if a.ComplianceStatus != "BLOCKED_REDLINE" || !strings.Contains(a.BlockedReason, "内部代号") {
		t.Fatalf("custom term not blocked: %+v", a)
	}
	// The configured file replaces the built-in list, so a term that is not
	// in it passes. That is the point of making the policy auditable.
	if a := redactForInvestigation("人肉开盒"); a.ComplianceStatus == "BLOCKED_REDLINE" {
		t.Fatalf("built-in term blocked although the file replaced the policy: %+v", a)
	}
}

func TestComplianceFileReadFailureFailsClosed(t *testing.T) {
	t.Setenv(ComplianceFileEnv, filepath.Join(t.TempDir(), "missing.txt"))
	a := redactForInvestigation("北京今天天气")
	if a.ComplianceStatus != "BLOCKED_REDLINE" {
		t.Fatalf("unreadable policy passed: %+v", a)
	}
	if !strings.Contains(a.BlockedReason, "fail-closed") {
		t.Fatalf("reason does not say fail-closed: %s", a.BlockedReason)
	}

	var ran atomic.Bool
	e := NewEngine()
	e.channelRunner = func(context.Context, string, Keys, int, map[string]bool) []runResult {
		ran.Store(true)
		return nil
	}
	inv := e.Investigate(context.Background(), Options{Query: "北京今天天气", ModeID: "M01"})
	if inv.Verdict != "ABSTAIN" {
		t.Fatalf("verdict = %s, want ABSTAIN", inv.Verdict)
	}
	if ran.Load() {
		t.Fatal("a channel ran although the compliance policy could not be read")
	}
	if len(inv.Citations) != 0 {
		t.Fatalf("citations = %d, want 0", len(inv.Citations))
	}
}

func TestComplianceFileWithNoTermsFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, []byte("# only comments\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ComplianceFileEnv, path)
	a := redactForInvestigation("北京今天天气")
	if a.ComplianceStatus != "BLOCKED_REDLINE" {
		t.Fatalf("empty policy passed: %+v", a)
	}
}

func TestRedactMasksEmailAndIPv4(t *testing.T) {
	a := Redact("联系 zhang.san@corp.cn 或 10.20.30.40，密钥 sk-Abc123Def456Ghi789Jkl012")
	if a.ComplianceStatus != "PASS (已通过时政与个人隐私红线校验)" {
		t.Fatalf("unexpected status: %+v", a)
	}
	for _, secret := range []string{"zhang.san@corp.cn", "10.20.30.40", "sk-Abc123Def456Ghi789Jkl012"} {
		if strings.Contains(a.MaskedQuery, secret) {
			t.Fatalf("%q survived: %s", secret, a.MaskedQuery)
		}
	}
	if !strings.Contains(a.MaskedQuery, "{{EMAIL_") || !strings.Contains(a.MaskedQuery, "{{API_KEY_") || !strings.Contains(a.MaskedQuery, "[IPV4]") {
		t.Fatalf("magpie's markers are missing: %s", a.MaskedQuery)
	}
	if a.MaskedCount != 3 {
		t.Fatalf("masked %d, want 3 (%v)", a.MaskedCount, a.Placeholders)
	}
}

// The input side is magpie's own finders, not a short regex list: an ID card
// with a valid checksum, a bank card that passes Luhn, a private key and a
// GitHub token all go as placeholders.
func TestRedactUsesMagpieFinders(t *testing.T) {
	const (
		id   = "110105199003078828"
		card = "6222020200112233446"
		key  = "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu\n-----END RSA PRIVATE KEY-----"
		gh   = "ghp_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8"
	)
	q := "身份证 " + id + " 卡号 " + card + " 私钥 " + key + " token " + gh
	a := Redact(q)
	if a.ComplianceStatus != "PASS (已通过时政与个人隐私红线校验)" {
		t.Fatalf("unexpected status: %+v", a)
	}
	for _, secret := range []string{id, card, "BEGIN RSA PRIVATE KEY", "ghp_0123456789"} {
		if strings.Contains(a.MaskedQuery, secret) {
			t.Fatalf("%q survived: %s", secret, a.MaskedQuery)
		}
	}
	if a.MaskedCount < 4 {
		t.Fatalf("masked %d, want at least 4 (%v)", a.MaskedCount, a.Placeholders)
	}
}
