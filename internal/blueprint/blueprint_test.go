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

[[task]]
id = "B"
title = "b"
deps = ["A"]
write_scope = ["b/**"]
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

func TestOverlap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bp.toml")
	_ = os.WriteFile(p, []byte(`
[[task]]
id = "A"
title = "a"
write_scope = ["src/**"]

[[task]]
id = "B"
title = "b"
write_scope = ["src/foo/**"]
`), 0o644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	errs := Validate(f)
	ok := false
	for _, e := range errs {
		if strings.Contains(e, "overlap") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("expected overlap, got %v", errs)
	}
}
