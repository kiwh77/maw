package blueprint

import (
	"strings"
	"testing"
)

func TestSanitizeWriteScopeDropsFragments(t *testing.T) {
	t.Parallel()
	clean, dropped := SanitizeWriteScope([]string{
		"backend/internal/capability/**",
		"限单包）",
		"pkg/**（限单包）",
		"scripts/baselines/integration-boundary.json",
		"说明文字没有路径",
	})
	if len(dropped) < 2 {
		t.Fatalf("expected drops, clean=%v dropped=%v", clean, dropped)
	}
	found := false
	for _, c := range clean {
		if c == "pkg/**" {
			found = true
		}
		if strings.Contains(c, "限单包") || strings.Contains(c, "说明") {
			t.Fatalf("fragment leaked into clean: %q", c)
		}
	}
	if !found {
		t.Fatalf("expected pkg/** after stripping note, got %v", clean)
	}
}

func TestValidateKeepsValidAfterDroppingFragment(t *testing.T) {
	raw := []byte(`
[meta]
name = "x"
version = 1
main_branch = "main"

[[task]]
id = "T-780"
title = "boundary"
write_scope = ["backend/internal/x/**", "限单包）"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 1
`)
	f, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	errs := Validate(f)
	for _, e := range errs {
		if strings.Contains(e, "write_scope required") {
			t.Fatalf("valid scope should remain: %v", errs)
		}
	}
	if len(f.Tasks[0].WriteScope) != 1 || f.Tasks[0].WriteScope[0] != "backend/internal/x/**" {
		t.Fatalf("scope=%v errs=%v", f.Tasks[0].WriteScope, errs)
	}
}
