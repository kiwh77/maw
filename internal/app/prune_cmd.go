package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/machine"
)

// PruneOptions 控制残留清理。
type PruneOptions struct {
	Yes            bool
	DryRun         bool
	ForceUnmerged  bool // 允许删除主干尚未包含 tip 的 agent 分支
}

// Prune lists or removes leftover worktrees and agent/* branches.
//
// 保留规则（kept_reasons 会写明）：
//   - ActiveOwner 任务占用的 worktree / 分支 / owner 树
//   - 非终态且仍挂 branch/worktree 的题（含 blocked），防止未合入代码被删
//   - status=working 的 Agent 树
//   - unknown / stop-unconfirmed / pending-acceptance 占用（blocked_holds）
//
// idle / offline、以及磁盘有树但无占用的目录，均可清（合入主干后不再温热保留）。
// Default is dry-run (yes=false); pass yes=true to delete.
// 若候选删除分支 tip 尚未合入主干，默认拒绝 --yes（需 --force-unmerged）。
func Prune(cwd string, yes, dryRun bool) (Result, error) {
	return PruneWith(cwd, PruneOptions{Yes: yes, DryRun: dryRun})
}

// PruneWith 带 ForceUnmerged 等扩展选项的 prune。
func PruneWith(cwd string, opts PruneOptions) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	preview := opts.DryRun || !opts.Yes

	var tasks []*db.Task
	var agents []*db.Agent
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		tasks, e = db.ListTasks(tx, pc.ID())
		if e != nil {
			return e
		}
		agents, e = db.ListAgents(tx, pc.ID())
		return e
	})
	if err != nil {
		return nil, err
	}

	protectedWT := map[string]bool{}
	protectedBranch := map[string]bool{}
	keptReasons := map[string]string{}
	blockedHolds := []string{}
	unmergedWarnings := []string{}

	for _, t := range tasks {
		if t == nil {
			continue
		}
		keep := machine.ActiveOwner[t.Status] || taskHasUnmergedArtifacts(t)
		if !keep {
			continue
		}
		reasonPrefix := "active_task:"
		if !machine.ActiveOwner[t.Status] {
			reasonPrefix = "unmerged_task:" + t.Status + ":"
		}
		if t.Worktree != "" {
			abs := absWorktree(pc.Repo, t.Worktree)
			protectedWT[abs] = true
			keptReasons[abs] = reasonPrefix + t.ID
		}
		if t.Branch != "" {
			protectedBranch[t.Branch] = true
			keptReasons["branch:"+t.Branch] = reasonPrefix + t.ID
		}
		if t.Owner != "" {
			abs := filepath.Join(pc.Repo, ".worktrees", t.Owner)
			protectedWT[abs] = true
			if _, ok := keptReasons[abs]; !ok {
				keptReasons[abs] = "active_owner:" + t.ID
			}
		}
	}
	for _, a := range agents {
		if a == nil || a.ID == "" {
			continue
		}
		abs := filepath.Join(pc.Repo, ".worktrees", a.ID)
		reason, keep := agentWorktreeKeepReason(a)
		if !keep {
			continue
		}
		protectedWT[abs] = true
		if _, ok := keptReasons[abs]; !ok {
			keptReasons[abs] = reason
		}
	}

	var removeWTs []string
	wtRoot := filepath.Join(pc.Repo, ".worktrees")
	if entries, err := os.ReadDir(wtRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			abs := filepath.Join(wtRoot, e.Name())
			if protectedWT[abs] {
				continue
			}
			holdReason := ""
			_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
				if reason, blocked := workspaceHoldReason(tx, abs); blocked {
					holdReason = reason
				}
				return nil
			})
			if holdReason != "" {
				protectedWT[abs] = true
				keptReasons[abs] = "hold:" + holdReason
				blockedHolds = append(blockedHolds, holdReason)
				continue
			}
			removeWTs = append(removeWTs, abs)
		}
	}

	if !preview && len(blockedHolds) > 0 && len(removeWTs) == 0 {
		return nil, errx.New("prune blocked by workspace hold",
			"unknown／残留进程／待验收占用解除前不能 prune："+strings.Join(blockedHolds, "; "))
	}

	mainBranch := pc.Config.Project.MainBranch
	if mainBranch == "" {
		mainBranch = "main"
	}

	var removeBranches []string
	out, _ := home.Git(pc.Repo, "branch", "--list", "agent/*")
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(strings.TrimPrefix(ln, "*"))
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if protectedBranch[ln] {
			continue
		}
		if !branchTipMerged(pc.Repo, mainBranch, ln) {
			msg := ln + " tip not in " + mainBranch
			unmergedWarnings = append(unmergedWarnings, msg)
			if !opts.ForceUnmerged {
				protectedBranch[ln] = true
				keptReasons["branch:"+ln] = "unmerged_branch"
				continue
			}
		}
		removeBranches = append(removeBranches, ln)
	}

	if !preview && len(unmergedWarnings) > 0 && !opts.ForceUnmerged && len(removeBranches) == 0 && len(removeWTs) == 0 {
		return nil, errx.New("prune blocked: unmerged agent branches",
			"以下分支 tip 尚未合入主干，拒绝删除（预览可见；确认后加 --force-unmerged）："+strings.Join(unmergedWarnings, "; "))
	}

	removedWT := []string{}
	removedBranches := []string{}
	if !preview {
		for _, wt := range removeWTs {
			skip := false
			_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
				if _, blocked := workspaceHoldReason(tx, wt); blocked {
					skip = true
				}
				return nil
			})
			if skip {
				blockedHolds = append(blockedHolds, "skipped occupied "+wt)
				continue
			}
			// 仅 maw 会话产物时也强制清掉，避免「看起来 dirty」挡住人工二次清理。
			_ = stripMawSessionArtifacts(wt)
			gitops.RemoveWorktree(pc.Repo, wt)
			removedWT = append(removedWT, wt)
		}
		for _, br := range removeBranches {
			for _, a := range agents {
				if a == nil {
					continue
				}
				agentWT := filepath.Join(pc.Repo, ".worktrees", a.ID)
				if !protectedWT[agentWT] {
					continue
				}
				if cur, err := gitops.CurrentBranch(agentWT); err == nil && cur == br {
					_ = gitops.DetachWorktree(agentWT)
				}
			}
			gitops.DeleteBranch(pc.Repo, br)
			removedBranches = append(removedBranches, br)
		}
		_, _ = home.Git(pc.Repo, "worktree", "prune")
	} else {
		removedWT = removeWTs
		removedBranches = removeBranches
	}

	next := "maw prune --yes  # 确认后删除上述残留"
	if len(unmergedWarnings) > 0 && !opts.ForceUnmerged {
		next = "存在未合入主干的 agent 分支（见 unmerged_warnings）；确认无用后 maw prune --yes --force-unmerged"
	}
	if !preview {
		next = "残留已清理；继续 maw next / maw board --serve"
	}
	if len(removeWTs) == 0 && len(removeBranches) == 0 {
		next = "无残留可清"
		if len(blockedHolds) > 0 {
			next = "占用未解除，无法 prune"
		}
		if len(unmergedWarnings) > 0 && !opts.ForceUnmerged {
			next = "仅有未合入分支被保护；确认后 --force-unmerged"
		}
	}

	return Result{
		"ok":                 true,
		"dry_run":            preview,
		"project_id":         pc.ID(),
		"worktrees":          removeWTs,
		"branches":           removeBranches,
		"removed_worktrees":  removedWT,
		"removed_branches":   removedBranches,
		"kept_worktrees":     len(protectedWT),
		"kept_reasons":       keptReasons,
		"blocked_holds":      blockedHolds,
		"unmerged_warnings":  unmergedWarnings,
		"force_unmerged":     opts.ForceUnmerged,
		"next":               next,
	}, nil
}

// taskHasUnmergedArtifacts 非终态且仍挂 branch/worktree → 禁止 prune 静默删除。
func taskHasUnmergedArtifacts(t *db.Task) bool {
	if t == nil {
		return false
	}
	if t.Status == "done" || t.Status == "cancelled" {
		return false
	}
	return t.Branch != "" || t.Worktree != ""
}

// branchTipMerged 报告 branch tip 是否已是 main 的祖先（已合入）。
func branchTipMerged(repo, mainBranch, branch string) bool {
	tip, err := home.Git(repo, "rev-parse", branch)
	if err != nil || strings.TrimSpace(tip) == "" {
		return true // 解析失败时不当未合并拦删（分支可能损坏）
	}
	_, err = home.Git(repo, "merge-base", "--is-ancestor", strings.TrimSpace(tip), mainBranch)
	return err == nil
}

func absWorktree(repo, relOrAbs string) string {
	if relOrAbs == "" {
		return ""
	}
	if filepath.IsAbs(relOrAbs) {
		return relOrAbs
	}
	return filepath.Join(repo, relOrAbs)
}

// agentWorktreeKeepReason 决定是否因 Agent 注册而保留其持久树。
// 仅 working 保留；idle / offline 在合入后可清。
func agentWorktreeKeepReason(a *db.Agent) (reason string, keep bool) {
	if a == nil || a.ID == "" {
		return "", false
	}
	if strings.TrimSpace(a.Status) == "working" {
		return "agent_working:" + a.ID, true
	}
	return "", false
}

// stripMawSessionArtifacts 删除 worktree 根上的 TASK.md 与 .maw/，
// 这些是领取时写入的会话文件，不应阻止清理。
func stripMawSessionArtifacts(wt string) error {
	if wt == "" {
		return nil
	}
	_ = os.Remove(filepath.Join(wt, "TASK.md"))
	_ = os.RemoveAll(filepath.Join(wt, ".maw"))
	return nil
}
