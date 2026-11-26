package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/version"
)

// Init 收敛为仓外 EnsureAttached：登记全局库并写 ~/.maw/projects/<id>/，
// 不向工程写入协议段、hooks、gitignore 或仓内 .multiagent。
func Init(cwd string, dryRun, upgrade bool) (Result, error) {
	repo, err := home.RepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	h, err := home.MawHome(cwd)
	if err != nil {
		return nil, err
	}
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	plan := Result{
		"repo": repo, "home": h, "global_db": gdb,
		"create": []string{"全局库 ~/.maw/maw.db", "仓外 ~/.maw/projects/<id>/", "config/blueprint"},
	}
	if dryRun {
		plan["dry_run"] = true
		plan["ok"] = true
		return plan, nil
	}
	repo, h, gdb, pid, pname, err := EnsureAttached(cwd, upgrade)
	if err != nil {
		return nil, err
	}
	doc, _ := Doctor(cwd)
	return Result{
		"ok": true, "home": h, "global_db": gdb, "project_id": pid, "project_name": pname,
		"repo": repo,
		"branch_protection_advice": []string{
			"保护主干：禁止 force push",
			"CI 跑 config.toml 中的 gates",
			"worker 勿直接推主干",
		},
		"doctor": doc,
		"next":   i18n.T("next.after_init"),
	}, nil
}

// Doctor 检查仓外接线与全局库；不再检查仓内 AGENTS.md / git hooks。
func Doctor(cwd string) (Result, error) {
	repo, err := home.RepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	issues := []string{}
	ok := true
	h, err := home.MawHome(cwd)
	if err != nil {
		return Result{"ok": false, "issues": []string{i18n.T("issue.not_init")}, "next": i18n.T("next.maw_init")}, nil
	}
	cfgPath := home.ConfigPath(h)
	if st, err := os.Stat(h); err != nil || !st.IsDir() {
		ok = false
		issues = append(issues, i18n.T("issue.not_init"))
	} else if _, err := os.Stat(cfgPath); err != nil {
		ok = false
		issues = append(issues, i18n.T("issue.not_init"))
	}
	gdb, err := home.GlobalDBPath()
	if err != nil {
		issues = append(issues, "无法定位全局库")
		ok = false
	} else if _, err := os.Stat(gdb); err != nil {
		issues = append(issues, "全局库缺失 ~/.maw/maw.db")
		ok = false
	}
	if warn := doctorInRepoConfigWarning(repo, cfgPath); warn != "" {
		// 仓内影子配置不阻断 ok，但必须可见，避免改错文件。
		issues = append(issues, warn)
	}
	next := "maw board --serve"
	if !ok {
		next = i18n.T("next.maw_init")
	}
	return Result{
		"ok": ok, "issues": issues, "maw_version": version.Version,
		"protocol_version": version.ProtocolVersion, "global_db": gdb,
		"home": h, "repo": home.CanonicalRepoPath(repo), "project_id": home.ProjectIDFromPath(home.CanonicalRepoPath(repo)),
		"config": cfgPath, "next": next,
	}, nil
}

// doctorInRepoConfigWarning 若仓内仍有 .multiagent/config.toml，提示 CLI 只读仓外路径；
// leads/main_branch 与生效配置不一致时升为 mismatch 文案。
func doctorInRepoConfigWarning(repo, effectiveCfg string) string {
	inRepo := filepath.Join(repo, home.MultiagentDir, home.ConfigName)
	if _, err := os.Stat(inRepo); err != nil {
		return ""
	}
	eff, errEff := config.Load(effectiveCfg)
	in, errIn := config.Load(inRepo)
	if errEff != nil || errIn != nil {
		return i18n.T("issue.inrepo_config_ignored", effectiveCfg)
	}
	effLeads := normalizeStrList(eff.Identity.Leads)
	inLeads := normalizeStrList(in.Identity.Leads)
	effMain := strings.TrimSpace(eff.Project.MainBranch)
	inMain := strings.TrimSpace(in.Project.MainBranch)
	if !reflect.DeepEqual(effLeads, inLeads) || effMain != inMain {
		return i18n.T("issue.inrepo_config_mismatch", effectiveCfg)
	}
	return i18n.T("issue.inrepo_config_ignored", effectiveCfg)
}

func normalizeStrList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ConfigPath 打印本机生效的工程 config.toml 绝对路径（杜绝改错仓内影子文件）。
func ConfigPath(cwd string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	path := home.ConfigPath(pc.LocalHome)
	return Result{
		"ok": true, "config": path, "home": pc.LocalHome, "project_id": pc.ID(),
		"note": "CLI 只读此路径；仓内 .multiagent/config.toml 不生效",
		"next": "编辑 [identity] leads / [project] main_branch 后重试相关命令",
	}, nil
}
