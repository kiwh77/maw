package protocol_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuhao/maw/internal/protocol"
)

func TestInstallFilesAlwaysWritesAgentsFallback(t *testing.T) {
	repo := t.TempDir()
	res, err := protocol.InstallFiles(repo)
	if err != nil {
		t.Fatal(err)
	}
	if res.Fallback != "AGENTS.md" {
		t.Fatalf("fallback: %s", res.Fallback)
	}
	if res.Files["AGENTS.md"] == "" {
		t.Fatal("AGENTS.md not installed")
	}
	b, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "maw:begin") {
		t.Fatalf("missing protocol segment: %s", b)
	}
	// 无痕迹时不应新建 CLAUDE.md
	if _, err := os.Stat(filepath.Join(repo, "CLAUDE.md")); err == nil {
		t.Fatal("should not create CLAUDE.md without detection")
	}
}

func TestInstallFilesWritesCursorWhenPresent(t *testing.T) {
	repo := t.TempDir()
	_ = os.MkdirAll(filepath.Join(repo, ".cursor"), 0o755)
	res, err := protocol.InstallFiles(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Detected["cursor"]; !ok {
		t.Fatalf("cursor not detected: %#v", res.Detected)
	}
	p := filepath.Join(repo, ".cursor/rules/maw.mdc")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "alwaysApply: true") || !strings.Contains(string(b), "maw:begin") {
		t.Fatalf("bad cursor rule: %s", b)
	}
}

func TestInstallFilesWritesQoderWhenPresent(t *testing.T) {
	repo := t.TempDir()
	_ = os.MkdirAll(filepath.Join(repo, ".qoder"), 0o755)
	res, err := protocol.InstallFiles(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Detected["qoder"]; !ok {
		t.Fatalf("qoder not detected: %#v", res.Detected)
	}
	p := filepath.Join(repo, ".qoder/rules/maw.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "trigger: always_on") {
		t.Fatalf("bad qoder rule: %s", b)
	}
}

func TestInstallFilesWritesTraeWhenPresent(t *testing.T) {
	repo := t.TempDir()
	_ = os.MkdirAll(filepath.Join(repo, ".trae"), 0o755)
	res, err := protocol.InstallFiles(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Detected["trae"]; !ok {
		t.Fatalf("trae not detected: %#v", res.Detected)
	}
	if _, err := os.Stat(filepath.Join(repo, ".trae/rules/maw.md")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallFilesClaudeOnlyIfExists(t *testing.T) {
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("# existing\n"), 0o644)
	res, err := protocol.InstallFiles(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Detected["claude"]; !ok {
		t.Fatalf("claude not detected: %#v", res.Detected)
	}
	b, _ := os.ReadFile(filepath.Join(repo, "CLAUDE.md"))
	if !strings.Contains(string(b), "existing") || !strings.Contains(string(b), "maw:begin") {
		t.Fatalf("should preserve + upsert: %s", b)
	}
}
