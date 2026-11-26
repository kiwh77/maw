package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/scope"
)

func HookPreCommit(cwd string) (Result, error) {
	if os.Getenv(gitops.MergeEnv) == "1" {
		return Result{"ok": true, "internal_merge": true}, nil
	}
	root, err := home.WorktreeRoot(cwd)
	if err != nil {
		return Result{"ok": true, "skipped": true}, nil
	}
	marker := filepath.Join(root, ".maw", "task.json")
	if _, err := os.Stat(marker); err != nil {
		// 无任务上下文时：仅当声明了 MAW_AGENT 才禁止在主干提交。
		// maw init 后人类日常提交不应被拦住；主干推送仍由 pre-push 保护。
		if strings.TrimSpace(os.Getenv("MAW_AGENT")) != "" {
			h, err := home.MawHome(cwd)
			if err == nil {
				if cfg, err := config.Load(home.ConfigPath(h)); err == nil {
					main := cfg.Project.MainBranch
					if br, _ := gitops.CurrentBranch(root); br == main {
						return nil, i18n.E("err.hook_trunk", "next.use_worktree", main)
					}
				}
			}
		}
		return Result{"ok": true, "skipped": true}, nil
	}
	b, _ := os.ReadFile(marker)
	var t db.Task
	_ = json.Unmarshal(b, &t)
	out, _ := home.Git(root, "diff", "--cached", "--name-only")
	var files []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || ln == "TASK.md" || strings.HasPrefix(ln, ".maw/") {
			continue
		}
		files = append(files, ln)
	}
	if len(files) == 0 {
		return Result{"ok": true}, nil
	}
	var bad []string
	for _, f := range files {
		if !scope.PathInScope(f, t.WriteScope) {
			bad = append(bad, f)
		}
	}
	if len(bad) > 0 {
		return nil, i18n.E("err.precommit_scope", "next.request_change", strings.Join(bad, ", "))
	}
	return Result{"ok": true, "checked": files}, nil
}

func HookPrePush(cwd string, stdin io.Reader) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(home.ConfigPath(h))
	if err != nil {
		return nil, err
	}
	main := cfg.Project.MainBranch
	payload, _ := io.ReadAll(stdin)
	for _, line := range strings.Split(string(payload), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		remoteRef := parts[2]
		if remoteRef == "refs/heads/"+main || strings.HasSuffix(remoteRef, "/"+main) {
			return nil, i18n.EN("err.prepush_ref", []any{remoteRef}, "next.only_merge_trunk", main)
		}
		if strings.HasPrefix(remoteRef, "refs/heads/") && !strings.HasPrefix(remoteRef, "refs/heads/agent/") {
			who, _ := identity.Resolve(cwd, "")
			if identity.Role(who, cfg) != "integrator" {
				return nil, i18n.E("err.prepush_branch", "next.push_agent", remoteRef)
			}
		}
	}
	return Result{"ok": true}, nil
}
