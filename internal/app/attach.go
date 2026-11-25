package app

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kiwh77/maw/internal/blueprint"
	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/version"
)

// EnsureAttached 将 git 仓登记进全局库，并确保仓外 home（config/blueprint）就绪。
// 不向工程树写任何文件，也不读取仓内 .multiagent / 协议段 / hooks。
func EnsureAttached(cwd string, upgrade bool) (repo, localHome, gdb, projectID, projectName string, err error) {
	repo, err = home.RepoRoot(cwd)
	if err != nil {
		return "", "", "", "", "", err
	}
	repo = home.CanonicalRepoPath(repo)
	localHome, err = home.MawHome(cwd)
	if err != nil {
		return "", "", "", "", "", err
	}
	// 未设置 MAW_HOME 时与 ProjectHome(repo) 对齐（已含路径规范化）
	if os.Getenv("MAW_HOME") == "" {
		if ph, e := home.ProjectHome(repo); e == nil {
			localHome = ph
		}
	}
	gdb, err = home.GlobalDBPath()
	if err != nil {
		return "", "", "", "", "", err
	}
	for _, d := range []string{localHome, filepath.Join(localHome, "board"), filepath.Join(localHome, "handoffs")} {
		if err = os.MkdirAll(d, 0o755); err != nil {
			return "", "", "", "", "", err
		}
	}
	decisions := filepath.Join(localHome, "DECISIONS.md")
	if _, e := os.Stat(decisions); os.IsNotExist(e) {
		_ = os.WriteFile(decisions, []byte("# Decisions\n"), 0o644)
	}
	cfgPath := home.ConfigPath(localHome)
	if _, e := os.Stat(cfgPath); os.IsNotExist(e) || upgrade {
		if err = os.WriteFile(cfgPath, []byte(config.RenderDefault(repo)), 0o644); err != nil {
			return "", "", "", "", "", err
		}
	}
	bp := home.BlueprintPath(localHome)
	if _, e := os.Stat(bp); os.IsNotExist(e) {
		_ = os.WriteFile(bp, []byte(blueprint.Example), 0o644)
	}
	if err = ensureWritableDB(gdb); err != nil {
		return "", "", "", "", "", err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return "", "", "", "", "", err
	}
	projectID = home.ProjectIDFromPath(repo)
	projectName = filepath.Base(repo)
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		_ = db.SetMeta(tx, "protocol_version", fmt.Sprint(version.ProtocolVersion))
		_ = db.SetMeta(tx, "maw_version", version.Version)
		p := &db.Project{
			ID: projectID, Name: projectName, Path: repo,
			MainBranch: cfg.Project.MainBranch, MaxParallel: cfg.Project.MaxParallel,
		}
		if e := db.UpsertProject(tx, p); e != nil {
			return e
		}
		return writeProjectMeta(localHome, p)
	})
	if err != nil {
		return "", "", "", "", "", err
	}
	return repo, localHome, gdb, projectID, projectName, nil
}

// ProjectAdd 显式登记当前仓（EnsureAttached），不领题。
func ProjectAdd(cwd string) (Result, error) {
	repo, local, gdb, pid, pname, err := EnsureAttached(cwd, false)
	if err != nil {
		return nil, err
	}
	return Result{
		"ok": true, "repo": repo, "home": local, "global_db": gdb,
		"project_id": pid, "project_name": pname,
		"next": i18n.T("next.after_init"),
	}, nil
}
