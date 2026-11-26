package identity

import (
	"os"
	"strings"
	"testing"
)

func TestProjectSlug(t *testing.T) {
	cases := map[string]string{
		"my-app":  "myapp",
		"DemoApp": "demoapp",
		"Foo_Bar": "foobar",
		"  X  ":   "x",
		"":        "project",
		"123abc":  "p123abc",
	}
	for in, want := range cases {
		if got := ProjectSlug(in); got != want {
			t.Fatalf("ProjectSlug(%q)=%q want %q", in, got, want)
		}
	}
}

func TestFormatParseCanonical(t *testing.T) {
	name := Format("Cursor", "my-app", 1)
	if name != "cursor-myapp-1" {
		t.Fatalf("got %s", name)
	}
	if !IsCanonical(name) {
		t.Fatal("expected canonical")
	}
	a, p, n, ok := Parse(name)
	if !ok || a != "cursor" || p != "myapp" || n != 1 {
		t.Fatalf("parse: %s %s %d %v", a, p, n, ok)
	}
	for _, bad := range []string{"w1", "cursor-admin-fe", "cursor-myapp-0", "Cursor-myapp-1"} {
		if IsCanonical(bad) {
			t.Fatalf("%s should not be canonical", bad)
		}
	}
}

func TestNextSeqAllocate(t *testing.T) {
	existing := []string{"w1", "cursor-myapp-1", "cursor-myapp-3", "codebuddy-myapp-1"}
	if got := NextSeq("cursor", "myapp", existing); got != 2 {
		t.Fatalf("next=%d want 2", got)
	}
	if got := Allocate("cursor", "my-app", existing); got != "cursor-myapp-2" {
		t.Fatalf("alloc=%s", got)
	}
	if got := Allocate("cursor", "my-app", nil); got != "cursor-myapp-1" {
		t.Fatalf("alloc empty=%s", got)
	}
}

func TestDetectAppMAWOverride(t *testing.T) {
	t.Setenv("MAW_APP", "Trae")
	if got := DetectApp(); got != "trae" {
		t.Fatalf("got %s", got)
	}
	_ = os.Unsetenv("MAW_APP")
}

// sanitizeAppEnv 清空所有应用判定相关环境变量，避免宿主进程的环境干扰用例。
// 注意必须真正 Unset：hasEnvPrefix 只匹配 "KEY=" 前缀，置空字符串仍会被判为存在。
func sanitizeAppEnv(t *testing.T) {
	t.Helper()
	prefixes := []string{"CURSOR_", "CODEBUDDY", "WORKBUDDY_", "QODER_", "TRAE_", "CLAUDE_", "WINDSURF_", "CODEIUM_"}
	keys := []string{"MAW_APP", "CLAUDECODE", "VSCODE_PROCESS_TITLE", "TERM_PROGRAM", "PATH"}
	for _, e := range os.Environ() {
		k, _, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(k, p) {
				keys = append(keys, k)
				break
			}
		}
	}
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
		if had {
			k, old := k, old
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		}
	}
}

// PATH 里带 claude 目录不得单独认定为 claude（CQ-001 回归用例）。
func TestDetectAppIgnoresClaudeInPath(t *testing.T) {
	sanitizeAppEnv(t)
	t.Setenv("PATH", "/Users/someone/.claude-code/bin:/usr/local/bin:/usr/bin:/bin")
	if got := DetectApp(); got == "claude" {
		t.Fatalf("PATH 含 claude 目录不应判成 claude，got %s", got)
	}
}

func TestDetectAppClaudeRuntimeEnv(t *testing.T) {
	cases := map[string]string{
		"CLAUDECODE":             "1",
		"CLAUDE_CODE_SSE_PORT":   "51234", // IDE 集成变量
		"CLAUDE_CODE_ENTRYPOINT": "claude-vscode",
	}
	for k, v := range cases {
		t.Run(k, func(t *testing.T) {
			sanitizeAppEnv(t)
			t.Setenv("PATH", "/Users/someone/.claude-code/bin:/usr/bin:/bin")
			t.Setenv(k, v)
			if got := DetectApp(); got != "claude" {
				t.Fatalf("%s=%s 应判为 claude，got %s", k, v, got)
			}
		})
	}
}

// MAW_APP 覆盖优先于任何环境推断。
func TestDetectAppOverrideBeatsClaudeEnv(t *testing.T) {
	sanitizeAppEnv(t)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("MAW_APP", "CodeBuddy")
	if got := DetectApp(); got != "codebuddy" {
		t.Fatalf("MAW_APP 应优先，got %s", got)
	}
}
