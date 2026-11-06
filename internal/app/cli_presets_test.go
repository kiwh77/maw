package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildCLIArgvPresets(t *testing.T) {
	dir := t.TempDir()
	// 让 LookPath 在测试里稳定命中假入口，不依赖本机是否装了这些 CLI。
	bin := map[string]string{}
	for _, name := range []string{"claude", "codex", "cursor-agent", "traecli", "codebuddy", "dsh"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolved = path
		}
		bin[name] = resolved
	}
	t.Setenv("PATH", dir)

	cases := []struct {
		cli        string
		permission string
		want       []string
	}{
		{"claude", "safe", []string{"claude", "-p", "--permission-mode", "default", taskPrompt}},
		{"claude", "unattended", []string{"claude", "-p", "--dangerously-skip-permissions", taskPrompt}},
		{"codex", "safe", []string{"codex", "exec", "-s", "workspace-write", taskPrompt}},
		{"codex", "unattended", []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", taskPrompt}},
		{"cursor", "unattended", []string{"cursor-agent", "-p", "--output-format", "text", "--force", taskPrompt}},
		{"traecli", "unattended", []string{"traecli", "exec", "--dangerously-bypass-approvals-and-sandbox", taskPrompt}},
		{"codebuddy", "unattended", []string{"codebuddy", "-p", "--output-format", "text", "-y", taskPrompt}},
		{"dsh", "safe", []string{"dsh", "--profile", "headless", taskPrompt}},
	}
	for _, c := range cases {
		argv, err := buildCLIArgv(c.cli, c.permission)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.cli, c.permission, err)
		}
		want := append([]string{bin[c.want[0]]}, c.want[1:]...)
		if !reflect.DeepEqual(argv, want) {
			t.Fatalf("%s/%s argv=%v want=%v", c.cli, c.permission, argv, want)
		}
	}
}

func TestWithModelArgs(t *testing.T) {
	meta := map[string]any{"cli_flavor": "codex", "default_model": "gpt-5", "prompt_last": true}
	argv := []string{"codex", "exec", taskPrompt}
	got := withModelArgs(argv, meta, "")
	want := []string{"codex", "exec", "-m", "gpt-5", taskPrompt}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codex model argv=%v want=%v", got, want)
	}

	got = withModelArgs(argv, meta, "gpt-5-mini")
	want = []string{"codex", "exec", "-m", "gpt-5-mini", taskPrompt}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codex override argv=%v want=%v", got, want)
	}

	claudeMeta := map[string]any{"cli_flavor": "claude", "default_model": "sonnet", "prompt_last": true}
	if got := withModelArgs([]string{"claude", "-p", taskPrompt}, claudeMeta, ""); !reflect.DeepEqual(got, []string{"claude", "-p", "--model", "sonnet", taskPrompt}) {
		t.Fatalf("claude argv=%v", got)
	}

	dshMeta := map[string]any{"cli_flavor": "dsh", "default_model": "deepseek-v4", "prompt_last": true}
	if got := withModelArgs([]string{"dsh", "--profile", "headless", taskPrompt}, dshMeta, ""); !reflect.DeepEqual(got, []string{"dsh", "--profile", "headless", taskPrompt}) {
		t.Fatalf("dsh argv should stay unchanged, got=%v", got)
	}
}
