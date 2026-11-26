package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLogPathRequiresHomeAbsolute(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	okPath := filepath.Join(home, "maw-logtail-test.log")
	got, err := ValidateLogPath(okPath)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("empty resolved path")
	}
	if _, err := ValidateLogPath("relative.log"); err == nil {
		t.Fatal("relative path must fail")
	}
	if _, err := ValidateLogPath("/tmp/outside-home.log"); err == nil {
		t.Fatal("path outside $HOME must fail")
	}
}

func TestReadLogFileOffsetAndTail(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".maw-test-logtail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "session.jsonl")
	body := strings.Repeat("abcdefghij", 10) // 100 bytes
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	chunk, err := ReadLogFile(path, 0, 20, false)
	if err != nil || !chunk.OK || chunk.Chunk != body[:20] || chunk.NextOffset != 20 || !chunk.Truncated {
		t.Fatalf("head chunk=%#v err=%v", chunk, err)
	}

	chunk, err = ReadLogFile(path, 20, 20, false)
	if err != nil || chunk.Chunk != body[20:40] || chunk.Offset != 20 {
		t.Fatalf("mid chunk=%#v err=%v", chunk, err)
	}

	chunk, err = ReadLogFile(path, 0, 30, true)
	if err != nil || !chunk.OK {
		t.Fatal(err)
	}
	if chunk.Offset != 70 || chunk.Chunk != body[70:] {
		t.Fatalf("tail want offset 70 got %#v", chunk)
	}
}

func TestAgentMetaLogPath(t *testing.T) {
	if AgentMetaLogPath(nil) != "" {
		t.Fatal()
	}
	if AgentMetaLogPath(map[string]any{"log_path": "/x"}) != "/x" {
		t.Fatal()
	}
}
