package protocol_test

import (
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/protocol"
	"github.com/kiwh77/maw/internal/version"
)

func TestClaimTemplateKeepsInstanceVersionSource(t *testing.T) {
	f := protocol.ClaimFields{
		TaskID: "VN-901", InstanceID: "ti_abc123", Version: 2,
		Source: "manual_next", AgentID: "cursor-demo-1",
		ProjectID: "0123456789abcdef", Worktree: ".worktrees/cursor-demo-1",
	}
	for _, lang := range []i18n.Lang{i18n.En, i18n.Zh} {
		tpl := protocol.RenderClaimTemplate(f, lang)
		for _, need := range []string{"ti_abc123", "VN-901", "manual_next", "2"} {
			if !strings.Contains(tpl, need) {
				t.Fatalf("lang=%s missing %q in:\n%s", lang, need, tpl)
			}
		}
		if protocol.ContainsSecret(tpl) {
			t.Fatalf("template must not contain secrets:\n%s", tpl)
		}
		if strings.Contains(tpl, "maw_pat_") || strings.Contains(strings.ToLower(tpl), "bearer ") {
			t.Fatalf("token leaked into template:\n%s", tpl)
		}
	}
}

func TestRedactSecretsStripsTokens(t *testing.T) {
	raw := "Authorization: Bearer maw_pat_5f7c2a9e1b and more"
	got := protocol.RedactSecrets(raw)
	if protocol.ContainsSecret(got) {
		t.Fatalf("still secret: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker: %q", got)
	}
}

func TestObserverCannotSeizeManagedRun(t *testing.T) {
	if protocol.ObserverMayClaimRun("cursor-observer-1", "cursor-owner-1", true) {
		t.Fatal("旁路不得夺取 managed Run")
	}
	if protocol.ObserverMayClaimRun("cursor-observer-1", "cursor-owner-1", false) {
		t.Fatal("旁路也不得通过 status 夺取非 managed 所有权")
	}
}

func TestUpgradeHintForStaleProtocol(t *testing.T) {
	i18n.Set(i18n.En)
	h := protocol.UpgradeHint(version.ProtocolVersion - 1)
	if h == "" || !strings.Contains(h, "init --upgrade") {
		t.Fatalf("en hint: %q", h)
	}
	i18n.Set(i18n.Zh)
	h = protocol.UpgradeHint(1)
	if h == "" || !strings.Contains(h, "init --upgrade") {
		t.Fatalf("zh hint: %q", h)
	}
	if protocol.UpgradeHint(version.ProtocolVersion) != "" {
		t.Fatal("current version needs no upgrade hint")
	}
}

func TestSegmentMentionsTokenAndBypassRules(t *testing.T) {
	en := protocol.SegmentLang(i18n.En)
	zh := protocol.SegmentLang(i18n.Zh)
	for _, seg := range []string{en, zh} {
		if !strings.Contains(seg, "Authorization") && !strings.Contains(seg, "令牌") {
			t.Fatalf("segment missing token rule:\n%s", seg[:200])
		}
	}
	if !strings.Contains(en, "managed Run") && !strings.Contains(en, "seize managed") {
		t.Fatalf("en missing bypass ownership rule:\n%s", en)
	}
	if !strings.Contains(zh, "managed Run") && !strings.Contains(zh, "夺取") {
		t.Fatalf("zh missing bypass ownership rule:\n%s", zh)
	}
	if !strings.Contains(en, "manual_next") || !strings.Contains(zh, "manual_next") {
		t.Fatal("both locales must mention manual_next source")
	}
}

func TestSegmentEnZhKeyIdeasSynced(t *testing.T) {
	en := protocol.SegmentLang(i18n.En)
	zh := protocol.SegmentLang(i18n.Zh)
	// 条数应对齐：两边都含 13. 开头的行
	enN := strings.Count(en, "\n13.")
	zhN := strings.Count(zh, "\n13.")
	if enN != 1 || zhN != 1 {
		t.Fatalf("expected rule 13 in both locales; en=%d zh=%d", enN, zhN)
	}
	for _, needle := range []string{"maw managed start", "status report", "write_scope"} {
		if !strings.Contains(en, needle) {
			t.Fatalf("en missing %q", needle)
		}
	}
	for _, needle := range []string{"maw managed start", "status report", "write_scope"} {
		if !strings.Contains(zh, needle) {
			t.Fatalf("zh missing %q", needle)
		}
	}
}
