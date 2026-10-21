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
	pathEnv := "PATH=" + filepath.Dir(bin) + string(os.PathListSeparator) + os.Getenv("PATH")
	baseEnv := append(os.Environ(), pathEnv)
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
merge_order = 1

[[task]]
id = "T-002"
title = "touch b"
priority = 1
deps = []
write_scope = ["mod_b/**"]
acceptance = ["file exists"]
merge_order = 2
`), 0o644)
	if out, err := run(nil, "plan", "apply", bp, "--yes"); err != nil {
		t.Fatalf("plan apply: %v\n%s", err, out)
	}

	if out, err := run([]string{"MAW_AGENT=w1"}, "next"); err != nil {
		t.Fatalf("next w1: %v\n%s", err, out)
	}
	wt1 := filepath.Join(repo, ".worktrees", "w1-T-001")
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
	wt2 := filepath.Join(repo, ".worktrees", "w2-T-002")
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

	if out, err := run([]string{"MAW_AGENT=int"}, "merge", "--yes"); err != nil {
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
}
