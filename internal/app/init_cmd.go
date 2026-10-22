package app

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wuhao/maw/internal/blueprint"
	"github.com/wuhao/maw/internal/config"
	"github.com/wuhao/maw/internal/db"
	"github.com/wuhao/maw/internal/home"
	"github.com/wuhao/maw/internal/hooks"
	"github.com/wuhao/maw/internal/protocol"
	"github.com/wuhao/maw/internal/version"
)

func Init(cwd string, dryRun, upgrade bool) (Result, error) {
	repo, err := home.RepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	h := filepath.Join(repo, home.MultiagentDir)
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	plan := Result{
		"repo": repo, "home": h, "global_db": gdb,
		"create": []string{"全局库 ~/.maw/maw.db", "工程 .multiagent/", "config/blueprint", "协议段", "hooks"},
	}
	if dryRun {
		plan["dry_run"] = true
		plan["ok"] = true
		return plan, nil
	}
	for _, d := range []string{h, filepath.Join(h, "board"), filepath.Join(h, "handoffs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	_ = os.WriteFile(filepath.Join(h, "DECISIONS.md"), []byte("# Decisions\n"), 0o644)
	cfgPath := home.ConfigPath(h)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) || upgrade {
		if err := os.WriteFile(cfgPath, []byte(config.RenderDefault(repo)), 0o644); err != nil {
			return nil, err
		}
	}
	bp := home.BlueprintPath(h)
	if _, err := os.Stat(bp); os.IsNotExist(err) {
		_ = os.WriteFile(bp, []byte(blueprint.Example), 0o644)
	}
	if err := db.Init(gdb); err != nil {
		return nil, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	pid := home.ProjectIDFromPath(repo)
	pname := filepath.Base(repo)
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		_ = db.SetMeta(tx, "protocol_version", fmt.Sprint(version.ProtocolVersion))
		_ = db.SetMeta(tx, "maw_version", version.Version)
		p := &db.Project{
			ID: pid, Name: pname, Path: repo,
			MainBranch: cfg.Project.MainBranch, MaxParallel: cfg.Project.MaxParallel,
		}
		if e := db.UpsertProject(tx, p); e != nil {
			return e
		}
		return writeProjectMeta(h, p)
	})
	if err != nil {
		return nil, err
	}
	_ = migrateLegacyLocalDB(h, gdb, pid)
	proto, err := protocol.InstallFiles(repo)
	if err != nil {
		return nil, err
	}
	_, _ = hooks.EnsureGitignore(repo)
	hookPaths, err := hooks.Install(repo)
	if err != nil {
		return nil, err
	}
	doc, _ := Doctor(cwd)
	return Result{
		"ok": true, "home": h, "global_db": gdb, "project_id": pid, "project_name": pname,
		"protocol": proto.Files, "protocol_detected": proto.Detected, "protocol_fallback": proto.Fallback,
		"hooks": hookPaths,
		"branch_protection_advice": []string{
			"保护主干：禁止 force push",
			"CI 跑 config.toml 中的 gates",
			"worker 勿直接推主干",
		},
		"doctor": doc,
		"next":   "拆任务清单并在蓝图 [meta] 指定 main_branch 后 `maw plan apply .multiagent/blueprint.toml --yes`；全局看板 `maw board --serve`",
	}, nil
}

func Doctor(cwd string) (Result, error) {
	repo, err := home.RepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	h, err := home.MawHome(cwd)
	issues := []string{}
	ok := true
	if err != nil {
		return Result{"ok": false, "issues": []string{"工程未 init"}, "next": "maw init"}, nil
	}
	if st, err := os.Stat(h); err != nil || !st.IsDir() {
		return Result{"ok": false, "issues": []string{"工程未 init"}, "next": "maw init"}, nil
	}
	gdb, err := home.GlobalDBPath()
	if err != nil {
		issues = append(issues, "无法定位全局库")
		ok = false
	} else if _, err := os.Stat(gdb); err != nil {
		issues = append(issues, "全局库缺失 ~/.maw/maw.db")
		ok = false
	}
	fileVer, found := protocol.ReadVersion(filepath.Join(repo, "AGENTS.md"))
	if !found {
		issues = append(issues, "AGENTS.md 协议段缺失")
		ok = false
	} else if fileVer != version.ProtocolVersion {
		issues = append(issues, fmt.Sprintf("协议版本 v%d != v%d", fileVer, version.ProtocolVersion))
		ok = false
	}
	hdir, _ := home.Git(repo, "rev-parse", "--git-path", "hooks")
	if hdir != "" && !filepath.IsAbs(hdir) {
		hdir = filepath.Join(repo, hdir)
	}
	for _, name := range []string{"pre-commit", "pre-push"} {
		if _, err := os.Stat(filepath.Join(hdir, name)); err != nil {
			issues = append(issues, "缺少 hook: "+name)
			ok = false
		}
	}
	next := "maw board --serve"
	if !ok {
		next = "maw init --upgrade"
	}
	return Result{
		"ok": ok, "issues": issues, "maw_version": version.Version,
		"protocol_version": version.ProtocolVersion, "global_db": gdb, "next": next,
	}, nil
}
