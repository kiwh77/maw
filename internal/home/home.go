package home

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/i18n"
)

const (
	// MultiagentDir 是历史仓内接线目录名；运行时不再使用，仅保留常量供 gitops 忽略路径等。
	MultiagentDir = ".multiagent"
	ProjectsDir   = "projects"
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

// RepoRoot 返回 cwd 所在 git 仓根。不再用 MAW_HOME 劫持仓库路径。
func RepoRoot(cwd string) (string, error) {
	common, err := Git(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", i18n.E("err.not_git_repo", "next.cd_init")
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

// CanonicalRepoPath 规范化仓库路径（Abs + EvalSymlinks），避免 /var 与 /private/var 分裂。
func CanonicalRepoPath(repo string) string {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return filepath.Clean(repo)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}

// ProjectHome 返回工程仓外本地目录：~/.maw/projects/<id>（或 MAW_GLOBAL 下同级）。
func ProjectHome(repo string) (string, error) {
	gd, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(gd, ProjectsDir, ProjectIDFromPath(CanonicalRepoPath(repo))), nil
}

// MawHome 工程本地目录（配置/蓝图），一律仓外。MAW_HOME 可覆盖为任意绝对路径（调试用）。
// 状态库在 GlobalDir。
func MawHome(cwd string) (string, error) {
	if env := os.Getenv("MAW_HOME"); env != "" {
		return filepath.Abs(env)
	}
	root, err := RepoRoot(cwd)
	if err != nil {
		return "", err
	}
	return ProjectHome(root)
}

// RequireHome 返回已存在的仓外 home；不存在则报未接线（调用方应先 EnsureAttached）。
func RequireHome(cwd string) (string, error) {
	h, err := MawHome(cwd)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(h); err != nil || !st.IsDir() {
		return "", i18n.E("err.project_not_wired", "next.run_init_root", h)
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

// LegacyLocalDBPath 旧版本地数据库路径（仅当误放在仓外 home 时仍可迁；不再读仓内）。
func LegacyLocalDBPath(home string) string {
	return filepath.Join(home, DBName)
}

func WorktreeRoot(cwd string) (string, error) {
	top, err := Git(cwd, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return "", i18n.E("err.not_worktree", "next.run_from_checkout")
	}
	return top, nil
}

// ProjectIDFromPath 由绝对路径生成稳定工程 ID。
// 调用方应传入已规范化路径（建议 RepoRoot / ProjectHome 内部已 EvalSymlinks）。
func ProjectIDFromPath(absPath string) string {
	sum := sha1.Sum([]byte(filepath.Clean(absPath)))
	return hex.EncodeToString(sum[:8])
}
