package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetMainBranchReplacesLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	orig := `[project]
main_branch = "main"
merge_strategy = "squash"
# keep me
max_parallel = 0
`
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetMainBranch(p, "dev"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, `main_branch = "dev"`) {
		t.Fatalf("expected main_branch=dev, got:\n%s", text)
	}
	if strings.Contains(text, `main_branch = "main"`) {
		t.Fatal("old main_branch should be gone")
	}
	if !strings.Contains(text, "# keep me") {
		t.Fatal("comments must be preserved")
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.MainBranch != "dev" {
		t.Fatalf("Load MainBranch=%q", cfg.Project.MainBranch)
	}
}

func TestSetMainBranchInsertsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("[project]\nmax_parallel = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetMainBranch(p, "trunk"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.MainBranch != "trunk" {
		t.Fatalf("got %q", cfg.Project.MainBranch)
	}
}
