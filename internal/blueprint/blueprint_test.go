package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCycle(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bp.toml")
	_ = os.WriteFile(p, []byte(`
[[task]]
id = "A"
title = "a"
deps = ["B"]
write_scope = ["a/**"]
acceptance = ["a ok"]
verify = ["manual: a"]

[[task]]
id = "B"
title = "b"
deps = ["A"]
write_scope = ["b/**"]
acceptance = ["b ok"]
verify = ["manual: b"]
`), 0o644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	errs := Validate(f)
	ok := false
	for _, e := range errs {
		if strings.Contains(e, "cycle") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("expected cycle, got %v", errs)
	}
}

func TestExternalDepsAllowed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bp.toml")
	_ = os.WriteFile(p, []byte(`
[[task]]
id = "T-110"
title = "wire"
priority = 10
deps = ["T-083"]
write_scope = ["app.go"]
acceptance = ["wired"]
verify = ["manual: wire"]
`), 0o644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	errs := Validate(f)
	for _, e := range errs {
		if strings.Contains(e, "unknown dependency") {
			t.Fatalf("增量蓝图应允许依赖历史题，got %v", errs)
		}
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected: %v", errs)
	}
}

func TestMainBranchWhitespaceRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bp.toml")
	_ = os.WriteFile(p, []byte(`
[meta]
name = "x"
main_branch = "dev branch"

[[task]]
id = "A"
title = "a"
write_scope = ["a/**"]
acceptance = ["a"]
verify = ["manual: a"]
`), 0o644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	errs := Validate(f)
	ok := false
	for _, e := range errs {
		if strings.Contains(e, "main_branch") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("expected main_branch whitespace error, got %v", errs)
	}
}

func TestMainBranchOptional(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bp.toml")
	_ = os.WriteFile(p, []byte(`
[[task]]
id = "A"
title = "a"
write_scope = ["a/**"]
acceptance = ["a"]
verify = ["manual: a"]
`), 0o644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if errs := Validate(f); len(errs) != 0 {
		t.Fatalf("main_branch optional for legacy blueprints, got %v", errs)
	}
}

func TestOverlapAllowedWhenPrioritized(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bp.toml")
	_ = os.WriteFile(p, []byte(`
[[task]]
id = "A"
title = "a"
priority = 1
write_scope = ["src/**"]
acceptance = ["a"]
verify = ["manual: a"]

[[task]]
id = "B"
title = "b"
priority = 2
deps = ["A"]
write_scope = ["src/foo/**"]
acceptance = ["b"]
verify = ["manual: b"]
`), 0o644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	errs := Validate(f)
	for _, e := range errs {
		if strings.Contains(e, "overlap") {
			t.Fatalf("overlap should be allowed (serialized by priority/deps), got %v", errs)
		}
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}
