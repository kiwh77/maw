package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

func TestRequiredPathsAndMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := &db.Task{
		Acceptance: []string{"required: ok.txt", "required: missing.kt", "基线：also_missing.md"},
		Extra:      map[string]any{"required_paths": []any{"ok.txt", "extra.gone"}},
	}
	paths := requiredPathsOf(task)
	if len(paths) < 3 {
		t.Fatalf("paths=%v", paths)
	}
	miss := missingRequiredPaths(dir, task)
	joined := miss
	if len(joined) < 2 {
		t.Fatalf("missing=%v", miss)
	}
	for _, m := range miss {
		if m == "ok.txt" {
			t.Fatal("ok.txt should exist")
		}
	}
}
