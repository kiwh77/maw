package home

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/errx"
)

const (
	MultiagentDir = ".multiagent"
	DBName        = "maw.db"
	ConfigName    = "config.toml"
	BlueprintName = "blueprint.toml"
	ProjectFile   = "project.json"
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

// MawHome 工程本地目录（配置/蓝图/hooks），状态库在 GlobalDir。
func MawHome(cwd string) (string, error) {
	if env := os.Getenv("MAW_HOME"); env != "" {
		p, _ := filepath.Abs(env)
		if filepath.Base(p) == MultiagentDir {
			return p, nil
		}
		cand := filepath.Join(p, MultiagentDir)
		if _, err := os.Stat(filepath.Join(p, ConfigName)); err == nil {
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
		return "", errx.New("工程未接入 maw（"+h+" 不存在）", "在仓库根目录运行 `maw init`")
	}
	return h, nil
}

// GlobalDir 系统级 maw 目录（默认 ~/.maw）
func GlobalDir() (string, error) {
	if env := os.Getenv("MAW_GLOBAL"); env != "" {
		return filepath.Abs(env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".maw"), nil
}

func GlobalDBPath() (string, error) {
	d, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, DBName), nil
}

func ConfigPath(home string) string    { return filepath.Join(home, ConfigName) }
func BlueprintPath(home string) string { return filepath.Join(home, BlueprintName) }
func ProjectMetaPath(home string) string {
	return filepath.Join(home, ProjectFile)
}

// LegacyLocalDBPath 旧版工程内数据库（用于迁移）
func LegacyLocalDBPath(home string) string {
	return filepath.Join(home, DBName)
}

func WorktreeRoot(cwd string) (string, error) {
	top, err := Git(cwd, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return "", errx.New("not inside a git worktree", "run from a git checkout")
	}
	return top, nil
}

// ProjectIDFromPath 由绝对路径生成稳定工程 ID
func ProjectIDFromPath(absPath string) string {
	sum := sha1.Sum([]byte(filepath.Clean(absPath)))
	return hex.EncodeToString(sum[:8])
}
