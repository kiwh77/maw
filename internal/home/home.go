package home

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wuhao/goal-agents/internal/errx"
)

const (
	MultiagentDir = ".multiagent"
	DBName        = "maw.db"
	ConfigName    = "config.toml"
	BlueprintName = "blueprint.toml"
)

func Git(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, err
	}
	return s, nil
}

func RepoRoot(cwd string) (string, error) {
	if env := os.Getenv("MAW_HOME"); env != "" {
		p, _ := filepath.Abs(env)
		if filepath.Base(p) == MultiagentDir {
			return filepath.Dir(p), nil
		}
		if st, err := os.Stat(filepath.Join(p, MultiagentDir)); err == nil && st.IsDir() {
			return p, nil
		}
		if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
			return p, nil
		}
	}
	common, err := Git(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", errx.New("not inside a git repository", "cd into a git repo, then run `maw init`")
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), nil
	}
	top, err := Git(cwd, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err == nil && top != "" {
		return top, nil
	}
	return filepath.Dir(common), nil
}

func MawHome(cwd string) (string, error) {
	if env := os.Getenv("MAW_HOME"); env != "" {
		p, _ := filepath.Abs(env)
		if filepath.Base(p) == MultiagentDir {
			return p, nil
		}
		cand := filepath.Join(p, MultiagentDir)
		if _, err := os.Stat(filepath.Join(p, DBName)); err == nil {
			return p, nil
		}
		return cand, nil
	}
	root, err := RepoRoot(cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, MultiagentDir), nil
}

func RequireHome(cwd string) (string, error) {
	h, err := MawHome(cwd)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(h); err != nil || !st.IsDir() {
		return "", errx.New("maw not initialized ("+h+" missing)", "run `maw init` in the repository root")
	}
	return h, nil
}

func DBPath(home string) string        { return filepath.Join(home, DBName) }
func ConfigPath(home string) string    { return filepath.Join(home, ConfigName) }
func BlueprintPath(home string) string { return filepath.Join(home, BlueprintName) }

func WorktreeRoot(cwd string) (string, error) {
	top, err := Git(cwd, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return "", errx.New("not inside a git worktree", "run from a git checkout")
	}
	return top, nil
}
