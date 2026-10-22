package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuhao/maw/internal/config"
	"github.com/wuhao/maw/internal/db"
	"github.com/wuhao/maw/internal/errx"
	"github.com/wuhao/maw/internal/gitops"
	"github.com/wuhao/maw/internal/home"
	"github.com/wuhao/maw/internal/identity"
	"github.com/wuhao/maw/internal/scope"
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
		h, err := home.MawHome(cwd)
		if err == nil {
			if cfg, err := config.Load(home.ConfigPath(h)); err == nil {
				main := cfg.Project.MainBranch
				if br, _ := gitops.CurrentBranch(root); br == main {
					return nil, errx.New("commits on trunk branch "+main+" are blocked by maw hook", "run `maw next` and work in the task worktree")
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
		return nil, errx.New("pre-commit: out of write_scope: "+strings.Join(bad, ", "), "maw request-change or unstage those files")
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
			return nil, errx.New("pre-push: refusing to push "+remoteRef, "only maw merge updates "+main+"; push agent/* branches only")
		}
		if strings.HasPrefix(remoteRef, "refs/heads/") && !strings.HasPrefix(remoteRef, "refs/heads/agent/") {
			who, _ := identity.Resolve(cwd, "")
			if identity.Role(who, cfg) != "integrator" {
				return nil, errx.New("pre-push: only agent/* branches allowed, got "+remoteRef, "push refs/heads/agent/<worker>/<task>")
			}
		}
	}
	return Result{"ok": true}, nil
}
