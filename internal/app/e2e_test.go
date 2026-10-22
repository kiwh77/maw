package app_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2EParallelFlow(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "maw")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/maw")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	repo := t.TempDir()
	globalDir := t.TempDir()
	pathEnv := "PATH=" + filepath.Dir(bin) + string(os.PathListSeparator) + os.Getenv("PATH")
	baseEnv := append(os.Environ(), pathEnv, "MAW_GLOBAL="+globalDir)
	run := func(env []string, args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir = repo
		c.Env = append(append([]string{}, baseEnv...), env...)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = baseEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git("init", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# proj\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(repo, "mod_a"), 0o755)
	_ = os.MkdirAll(filepath.Join(repo, "mod_b"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "mod_a", ".gitkeep"), []byte{}, 0o644)
	_ = os.WriteFile(filepath.Join(repo, "mod_b", ".gitkeep"), []byte{}, 0o644)
	git("add", ".")
	git("commit", "-m", "init")

	if out, err := run(nil, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	bp := filepath.Join(repo, "bp.toml")
	_ = os.WriteFile(bp, []byte(`
[meta]
name = "demo"
version = 1

[[task]]
id = "T-001"
title = "touch a"
priority = 1
deps = []
write_scope = ["mod_a/**"]
acceptance = ["file exists"]
verify = ["manual: file exists"]
merge_order = 1

[[task]]
id = "T-002"
title = "touch b"
priority = 1
deps = []
write_scope = ["mod_b/**"]
acceptance = ["file exists"]
verify = ["manual: file exists"]
merge_order = 2
`), 0o644)
	if out, err := run(nil, "plan", "apply", bp, "--yes"); err != nil {
		t.Fatalf("plan apply: %v\n%s", err, out)
	}

	if out, err := run([]string{"MAW_AGENT=w1"}, "next"); err != nil {
		t.Fatalf("next w1: %v\n%s", err, out)
	}
	wt1 := filepath.Join(repo, ".worktrees", "w1")
	_ = os.WriteFile(filepath.Join(wt1, "mod_a", "a.txt"), []byte("a\n"), 0o644)
	c := exec.Command("git", "add", "mod_a/a.txt")
	c.Dir = wt1
	c.Env = baseEnv
	_ = c.Run()
	c = exec.Command("git", "commit", "-m", "a")
	c.Dir = wt1
	c.Env = baseEnv
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("commit a: %v\n%s", err, out)
	}
	c = exec.Command(bin, "submit")
	c.Dir = wt1
	c.Env = append(baseEnv, "MAW_AGENT=w1")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("submit w1: %v\n%s", err, out)
	}

	if out, err := run([]string{"MAW_AGENT=w2"}, "next"); err != nil {
		t.Fatalf("next w2: %v\n%s", err, out)
	}
	wt2 := filepath.Join(repo, ".worktrees", "w2")
	_ = os.WriteFile(filepath.Join(wt2, "mod_b", "b.txt"), []byte("b\n"), 0o644)
	c = exec.Command("git", "add", "mod_b/b.txt")
	c.Dir = wt2
	c.Env = baseEnv
	_ = c.Run()
	c = exec.Command("git", "commit", "-m", "b")
	c.Dir = wt2
	c.Env = baseEnv
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("commit b: %v\n%s", err, out)
	}
	c = exec.Command(bin, "submit")
	c.Dir = wt2
	c.Env = append(baseEnv, "MAW_AGENT=w2")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("submit w2: %v\n%s", err, out)
	}

	if out, err := run([]string{"MAW_AGENT=w1"}, "merge", "--yes"); err != nil {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	out, err := run(nil, "--json", "board")
	if err != nil {
		t.Fatalf("board: %v\n%s", err, out)
	}
	if !strings.Contains(out, "done") {
		t.Fatalf("board missing done: %s", out)
	}
	log, _ := exec.Command("git", "-C", repo, "log", "--oneline").CombinedOutput()
	if !strings.Contains(string(log), "maw: merge") {
		t.Fatalf("expected merge commits, got %s", log)
	}
	// 合入后保留 per-agent worktree，仅删除 agent 题分支
	if st, err := os.Stat(wt1); err != nil || !st.IsDir() {
		t.Fatalf("worktree should be kept after merge: %s (%v)", wt1, err)
	}
	if st, err := os.Stat(wt2); err != nil || !st.IsDir() {
		t.Fatalf("worktree should be kept after merge: %s (%v)", wt2, err)
	}
	branches, _ := exec.Command("git", "-C", repo, "branch", "--list", "agent/*").CombinedOutput()
	if strings.TrimSpace(string(branches)) != "" {
		t.Fatalf("agent branches should be deleted after merge, still have:\n%s", branches)
	}
}

func TestE2EPerAgentWorktreeReuse(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "maw")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/maw")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	repo := t.TempDir()
	globalDir := t.TempDir()
	pathEnv := "PATH=" + filepath.Dir(bin) + string(os.PathListSeparator) + os.Getenv("PATH")
	baseEnv := append(os.Environ(), pathEnv, "MAW_GLOBAL="+globalDir)
	run := func(env []string, args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir = repo
		c.Env = append(append([]string{}, baseEnv...), env...)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = baseEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commitIn := func(wt, rel, content, msg string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(wt, rel)), 0o755)
		_ = os.WriteFile(filepath.Join(wt, rel), []byte(content), 0o644)
		c := exec.Command("git", "add", rel)
		c.Dir = wt
		c.Env = baseEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git add: %v\n%s", err, out)
		}
		c = exec.Command("git", "commit", "-m", msg)
		c.Dir = wt
		c.Env = baseEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v\n%s", err, out)
		}
	}

	git("init", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# proj\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(repo, "mod_a"), 0o755)
	_ = os.MkdirAll(filepath.Join(repo, "mod_b"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "mod_a", ".gitkeep"), []byte{}, 0o644)
	_ = os.WriteFile(filepath.Join(repo, "mod_b", ".gitkeep"), []byte{}, 0o644)
	git("add", ".")
	git("commit", "-m", "init")

	if out, err := run(nil, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	bp := filepath.Join(repo, "bp.toml")
	_ = os.WriteFile(bp, []byte(`
[meta]
name = "demo"
version = 1

[[task]]
id = "T-001"
title = "touch a"
priority = 1
deps = []
write_scope = ["mod_a/**"]
acceptance = ["file exists"]
verify = ["manual: file exists"]
merge_order = 1

[[task]]
id = "T-002"
title = "touch b"
priority = 2
deps = ["T-001"]
write_scope = ["mod_b/**"]
acceptance = ["file exists"]
verify = ["manual: file exists"]
merge_order = 2
`), 0o644)
	if out, err := run(nil, "plan", "apply", bp, "--yes"); err != nil {
		t.Fatalf("plan apply: %v\n%s", err, out)
	}

	wt := filepath.Join(repo, ".worktrees", "w1")
	ignored := filepath.Join(wt, "node_modules", "keep.txt")

	if out, err := run([]string{"MAW_AGENT=w1"}, "next"); err != nil {
		t.Fatalf("next T-001: %v\n%s", err, out)
	}
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		t.Fatalf("expected per-agent worktree %s", wt)
	}
	_ = os.MkdirAll(filepath.Dir(ignored), 0o755)
	_ = os.WriteFile(ignored, []byte("deps\n"), 0o644)
	commitIn(wt, "mod_a/a.txt", "a\n", "a")
	c := exec.Command(bin, "submit")
	c.Dir = wt
	c.Env = append(baseEnv, "MAW_AGENT=w1")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("submit T-001: %v\n%s", err, out)
	}
	if out, err := run([]string{"MAW_AGENT=w1"}, "merge", "--yes"); err != nil {
		t.Fatalf("merge T-001: %v\n%s", err, out)
	}
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		t.Fatalf("worktree should survive merge: %v", err)
	}
	if b, err := os.ReadFile(ignored); err != nil || string(b) != "deps\n" {
		t.Fatalf("ignored deps should survive merge: %v %q", err, b)
	}

	if out, err := run([]string{"MAW_AGENT=w1"}, "next"); err != nil {
		t.Fatalf("next T-002: %v\n%s", err, out)
	}
	// Same path reused
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		t.Fatalf("reused worktree missing: %v", err)
	}
	if b, err := os.ReadFile(ignored); err != nil || string(b) != "deps\n" {
		t.Fatalf("ignored deps should survive next reset: %v %q", err, b)
	}
	// Second task branch from main tip should already include a.txt from merge
	if _, err := os.Stat(filepath.Join(wt, "mod_a", "a.txt")); err != nil {
		t.Fatalf("reset to main tip should include merged a.txt: %v", err)
	}
	br, _ := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "HEAD").CombinedOutput()
	if !strings.Contains(string(br), "agent/w1/T-002") {
		t.Fatalf("expected branch agent/w1/T-002, got %s", br)
	}
	commitIn(wt, "mod_b/b.txt", "b\n", "b")
	c = exec.Command(bin, "submit")
	c.Dir = wt
	c.Env = append(baseEnv, "MAW_AGENT=w1")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("submit T-002: %v\n%s", err, out)
	}
	if out, err := run([]string{"MAW_AGENT=w1"}, "merge", "--yes"); err != nil {
		t.Fatalf("merge T-002: %v\n%s", err, out)
	}
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		t.Fatalf("worktree should still exist after second merge: %v", err)
	}
	branches, _ := exec.Command("git", "-C", repo, "branch", "--list", "agent/*").CombinedOutput()
	if strings.TrimSpace(string(branches)) != "" {
		t.Fatalf("agent branches should be deleted, still have:\n%s", branches)
	}

	// Seed a legacy leftover and ensure prune removes it while keeping agent tree
	legacy := filepath.Join(repo, ".worktrees", "w1-T-OLD")
	_ = os.MkdirAll(legacy, 0o755)
	_ = os.WriteFile(filepath.Join(legacy, "junk"), []byte("x"), 0o644)
	preview, err := run(nil, "--json", "prune")
	if err != nil {
		t.Fatalf("prune preview: %v\n%s", err, preview)
	}
	if !strings.Contains(preview, "w1-T-OLD") {
		t.Fatalf("prune should list legacy worktree: %s", preview)
	}
	if out, err := run(nil, "prune", "--yes"); err != nil {
		t.Fatalf("prune --yes: %v\n%s", err, out)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy worktree should be removed, err=%v", err)
	}
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		t.Fatalf("per-agent worktree should be kept by prune: %v", err)
	}
}

func TestE2ESubmitRejectsDirtyWorktree(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "maw")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/maw")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	repo := t.TempDir()
	globalDir := t.TempDir()
	pathEnv := "PATH=" + filepath.Dir(bin) + string(os.PathListSeparator) + os.Getenv("PATH")
	baseEnv := append(os.Environ(), pathEnv, "MAW_GLOBAL="+globalDir)
	run := func(env []string, args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir = repo
		c.Env = append(append([]string{}, baseEnv...), env...)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	git := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = baseEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git(repo, "init", "-b", "main")
	git(repo, "config", "user.email", "t@t")
	git(repo, "config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# proj\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(repo, "mod_a"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "mod_a", ".gitkeep"), []byte{}, 0o644)
	git(repo, "add", ".")
	git(repo, "commit", "-m", "init")

	if out, err := run(nil, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	bp := filepath.Join(repo, "bp.toml")
	_ = os.WriteFile(bp, []byte(`
[meta]
name = "dirty-demo"
version = 1

[[task]]
id = "T-001"
title = "touch a"
priority = 1
deps = []
write_scope = ["mod_a/**"]
acceptance = ["file exists"]
verify = ["manual: file exists"]
merge_order = 1
`), 0o644)
	if out, err := run(nil, "plan", "apply", bp, "--yes"); err != nil {
		t.Fatalf("plan apply: %v\n%s", err, out)
	}
	if out, err := run([]string{"MAW_AGENT=w1"}, "next"); err != nil {
		t.Fatalf("next: %v\n%s", err, out)
	}
	wt := filepath.Join(repo, ".worktrees", "w1")
	_ = os.WriteFile(filepath.Join(wt, "mod_a", "a.txt"), []byte("a\n"), 0o644)
	git(wt, "add", "mod_a/a.txt")
	// staged but not committed → submit must fail, stay in_progress
	c := exec.Command(bin, "submit")
	c.Dir = wt
	c.Env = append(baseEnv, "MAW_AGENT=w1")
	out, err := c.CombinedOutput()
	if err == nil {
		t.Fatalf("expected dirty submit to fail, got: %s", out)
	}
	if !strings.Contains(string(out), "working tree dirty") {
		t.Fatalf("expected dirty error, got: %s", out)
	}
	board, _ := run(nil, "--json", "board")
	if !strings.Contains(board, `"in_progress": 1`) && !strings.Contains(board, `"in_progress":1`) {
		t.Fatalf("expected still in_progress count=1: %s", board)
	}
	if strings.Contains(board, `"merging":`) || strings.Contains(board, `"conflict":`) {
		// counts 里若出现 merging/conflict 且非 0 则失败；宽松：terminal 不得写合并冲突题
		if strings.Contains(board, "【冲突】") || strings.Contains(board, "【待合入】") {
			t.Fatalf("dirty submit must not advance toward merge/conflict: %s", board)
		}
	}

	git(wt, "commit", "-m", "a")
	c = exec.Command(bin, "submit")
	c.Dir = wt
	c.Env = append(baseEnv, "MAW_AGENT=w1")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("submit after commit: %v\n%s", err, out)
	}
	if out, err := run([]string{"MAW_AGENT=w1"}, "merge", "--yes"); err != nil {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	board, _ = run(nil, "--json", "board")
	if !strings.Contains(board, `"done": 1`) && !strings.Contains(board, `"done":1`) {
		t.Fatalf("expected done count=1 after merge: %s", board)
	}
	// done 题不应再带 blocked_reason（合入成功已清空）
	if strings.Contains(board, "working tree dirty") {
		t.Fatalf("done task must not surface dirty blocked_reason: %s", board)
	}
}
