package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuhao/goal-agents/internal/blueprint"
	"github.com/wuhao/goal-agents/internal/board"
	"github.com/wuhao/goal-agents/internal/config"
	"github.com/wuhao/goal-agents/internal/db"
	"github.com/wuhao/goal-agents/internal/errx"
	"github.com/wuhao/goal-agents/internal/gitops"
	"github.com/wuhao/goal-agents/internal/home"
	"github.com/wuhao/goal-agents/internal/hooks"
	"github.com/wuhao/goal-agents/internal/identity"
	"github.com/wuhao/goal-agents/internal/machine"
	"github.com/wuhao/goal-agents/internal/protocol"
	"github.com/wuhao/goal-agents/internal/scope"
	"github.com/wuhao/goal-agents/internal/version"
)

type Result map[string]any

func refreshBoard(h string) (string, error) {
	cfg, err := config.Load(home.ConfigPath(h))
	if err != nil {
		return "", err
	}
	var tasks []*db.Task
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		var e error
		tasks, e = db.ListTasks(tx)
		return e
	})
	if err != nil {
		return "", err
	}
	return board.Write(h, tasks, cfg.Project.MaxParallel)
}

func writeTaskCard(wt string, t *db.Task, agent string) error {
	dir := filepath.Join(wt, ".maw")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "agent"), []byte(agent+"\n"), 0o644); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(t, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "task.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	var scopeLines, accLines []string
	for _, s := range t.WriteScope {
		scopeLines = append(scopeLines, "- `"+s+"`")
	}
	for _, a := range t.Acceptance {
		accLines = append(accLines, "- "+a)
	}
	if len(accLines) == 0 {
		accLines = []string{"- (none)"}
	}
	md := fmt.Sprintf("# %s: %s\n\n## Owner\n`%s`\n\n## Write scope\n%s\n\n## Acceptance\n%s\n\n## Protocol\nRun `maw progress -m \"...\"`, then `maw submit`. Do not merge to main.\n",
		t.ID, t.Title, agent, strings.Join(scopeLines, "\n"), strings.Join(accLines, "\n"))
	return os.WriteFile(filepath.Join(wt, "TASK.md"), []byte(md), 0o644)
}

func inferTaskID(cwd string) (string, error) {
	root, err := home.WorktreeRoot(cwd)
	if err != nil {
		if cwd != "" {
			root = cwd
		} else {
			root, _ = os.Getwd()
		}
	}
	b, err := os.ReadFile(filepath.Join(root, ".maw", "task.json"))
	if err != nil {
		return "", errx.New("cannot infer task from worktree", "cd into the task worktree from `maw next`, or pass --task")
	}
	var t db.Task
	if err := json.Unmarshal(b, &t); err != nil || t.ID == "" {
		return "", errx.New("invalid .maw/task.json", "re-run `maw next`")
	}
	return t.ID, nil
}

func promoteReady(tx *sql.Tx) error {
	tasks, err := db.ListTasks(tx)
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for _, t := range tasks {
		if t.Status == "done" {
			done[t.ID] = true
		}
	}
	for _, t := range tasks {
		if t.Status != "backlog" && t.Status != "ready" {
			continue
		}
		ok := true
		for _, d := range t.Deps {
			if !done[d] {
				ok = false
				break
			}
		}
		if ok && t.Status != "ready" {
			if err := machine.AssertTransition(t.Status, "ready"); err != nil {
				return err
			}
			t.Status = "ready"
			t.Revision++
			if err := db.UpsertTask(tx, t); err != nil {
				return err
			}
		}
	}
	return nil
}

func Init(cwd string, dryRun, upgrade bool) (Result, error) {
	repo, err := home.RepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	h := filepath.Join(repo, home.MultiagentDir)
	plan := Result{
		"repo": repo, "home": h,
		"create": []string{"maw.db", "config.toml", "blueprint.toml", "board", "handoffs", "AGENTS.md", "hooks", ".gitignore"},
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
	if err := db.Init(home.DBPath(h)); err != nil {
		return nil, err
	}
	_ = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		_ = db.SetMeta(tx, "protocol_version", fmt.Sprint(version.ProtocolVersion))
		return db.SetMeta(tx, "maw_version", version.Version)
	})
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
		"ok": true, "home": h, "protocol": proto, "hooks": hookPaths,
		"branch_protection_advice": []string{
			"Protect main branch: require PR, disallow force push",
			"Add CI job running project gates from config.toml",
			"Do not grant workers push access to main",
		},
		"doctor": doc,
		"next":   "edit .multiagent/blueprint.toml then `maw plan apply .multiagent/blueprint.toml --yes`",
	}, nil
}

func Doctor(cwd string) (Result, error) {
	repo, err := home.RepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	h, err := home.MawHome(cwd)
	if err != nil {
		return Result{"ok": false, "issues": []string{"not initialized"}, "next": "maw init"}, nil
	}
	issues := []string{}
	ok := true
	if st, err := os.Stat(h); err != nil || !st.IsDir() {
		return Result{"ok": false, "issues": []string{"not initialized"}, "next": "maw init"}, nil
	}
	if _, err := os.Stat(home.DBPath(h)); err != nil {
		issues = append(issues, "maw.db missing")
		ok = false
	} else {
		d, err := db.Open(home.DBPath(h))
		if err == nil {
			pv, _ := db.GetMeta(d, "protocol_version")
			_ = d.Close()
			if pv != "" && pv != fmt.Sprint(version.ProtocolVersion) {
				issues = append(issues, "db protocol "+pv+" != code")
				ok = false
			}
		}
	}
	if v, found := protocol.ReadVersion(filepath.Join(repo, "AGENTS.md")); !found {
		issues = append(issues, "AGENTS.md protocol segment missing")
		ok = false
	} else if v != version.ProtocolVersion {
		issues = append(issues, fmt.Sprintf("AGENTS.md protocol v%d != v%d", v, version.ProtocolVersion))
		ok = false
	}
	hdir, _ := home.Git(repo, "rev-parse", "--git-path", "hooks")
	if !filepath.IsAbs(hdir) {
		hdir = filepath.Join(repo, hdir)
	}
	for _, name := range []string{"pre-commit", "pre-push"} {
		if _, err := os.Stat(filepath.Join(hdir, name)); err != nil {
			issues = append(issues, "hook missing: "+name)
			ok = false
		}
	}
	next := "maw board"
	if !ok {
		next = "maw init --upgrade"
	}
	return Result{"ok": ok, "issues": issues, "maw_version": version.Version, "protocol_version": version.ProtocolVersion, "next": next}, nil
}

func PlanCheck(path, cwd string) (Result, error) {
	f, err := blueprint.Load(path)
	if err != nil {
		return nil, err
	}
	errs := blueprint.Validate(f)
	next := "fix blueprint errors"
	if len(errs) == 0 {
		next = "maw plan apply " + path + " --yes"
	}
	return Result{"ok": len(errs) == 0, "errors": errs, "task_count": len(f.Tasks), "next": next}, nil
}

func PlanApply(path, cwd string, yes, dryRun bool) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(home.ConfigPath(h))
	if err != nil {
		return nil, err
	}
	f, err := blueprint.Load(path)
	if err != nil {
		return nil, err
	}
	if errs := blueprint.Validate(f); len(errs) > 0 {
		return nil, errx.New("blueprint validation failed:\n- "+strings.Join(errs, "\n- "), "fix write_scope overlaps / deps")
	}
	tasks := blueprint.TasksForDB(f)
	if dryRun {
		return Result{"ok": true, "dry_run": true, "tasks": tasks}, nil
	}
	if cfg.Approvals.Plan == "manual" && !yes {
		return nil, errx.New("plan apply requires confirmation", "review tasks then re-run with --yes")
	}
	dest := home.BlueprintPath(h)
	if abs, _ := filepath.Abs(path); abs != dest {
		b, _ := os.ReadFile(path)
		_ = os.WriteFile(dest, b, 0o644)
	}
	agent := "orchestrator"
	if a, err := identity.Resolve(cwd, ""); err == nil {
		agent = a
	}
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM tasks`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM shared_files`); err != nil {
			return err
		}
		for _, t := range tasks {
			if err := db.UpsertTask(tx, t); err != nil {
				return err
			}
			_ = db.AppendEvent(tx, agent, "task_created", t.ID, map[string]any{"status": t.Status})
		}
		for _, sf := range f.SharedFiles {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO shared_files(path, owner_task) VALUES (?,?)`, sf.Path, sf.OwnerTask); err != nil {
				return err
			}
		}
		if err := promoteReady(tx); err != nil {
			return err
		}
		return db.AppendEvent(tx, agent, "plan_applied", "", map[string]any{"path": path})
	})
	if err != nil {
		return nil, err
	}
	var out []*db.Task
	_ = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		out, err = db.ListTasks(tx)
		return err
	})
	html, _ := refreshBoard(h)
	return Result{"ok": true, "tasks": out, "board": html, "next": "export MAW_AGENT=w1 && maw next"}, nil
}

func Next(cwd, agentFlag string, dryRun bool) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	repo, err := home.RepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(home.ConfigPath(h))
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, agentFlag)
	if err != nil {
		return nil, err
	}
	var task *db.Task
	var branch, wtRel string
	idempotent := false
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		tasks, err := db.ListTasks(tx)
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.Owner == who && machine.ActiveOwner[t.Status] {
				task = t
				idempotent = true
				return nil
			}
		}
		active := 0
		for _, t := range tasks {
			if machine.ActiveOwner[t.Status] {
				active++
			}
		}
		if active >= cfg.Project.MaxParallel {
			return errx.New(fmt.Sprintf("max_parallel=%d reached", cfg.Project.MaxParallel), "wait for submit/merge")
		}
		row := tx.QueryRow(`SELECT id FROM tasks WHERE status='ready' ORDER BY priority ASC, merge_order ASC, id ASC LIMIT 1`)
		var id string
		if err := row.Scan(&id); err == sql.ErrNoRows {
			return errx.New("no ready tasks", "run `maw board`; finish deps or `maw plan apply`")
		} else if err != nil {
			return err
		}
		t, err := db.GetTask(tx, id)
		if err != nil || t == nil {
			return err
		}
		if dryRun {
			task = t
			return nil
		}
		if err := machine.AssertTransition(t.Status, "in_progress"); err != nil {
			return err
		}
		_ = db.AppendEvent(tx, who, "claimed", t.ID, nil)
		branch = "agent/" + who + "/" + t.ID
		wtRel = filepath.Join(".worktrees", who+"-"+t.ID)
		base, err := gitops.EnsureBranchBase(repo, cfg.Project.MainBranch)
		if err != nil {
			return err
		}
		wt := filepath.Join(repo, wtRel)
		if err := gitops.CreateWorktree(repo, wt, branch, base); err != nil {
			return err
		}
		t.Status = "in_progress"
		t.Owner = who
		t.Branch = branch
		t.Worktree = wtRel
		t.Heartbeat = db.UTCNow()
		t.Revision++
		t.Attempts++
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		_ = db.AppendEvent(tx, who, "status_changed", t.ID, map[string]any{"to": "in_progress"})
		if err := writeTaskCard(wt, t, who); err != nil {
			return err
		}
		task = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	if idempotent {
		return Result{
			"ok": true, "idempotent": true, "task": task,
			"worktree": filepath.Join(repo, task.Worktree),
			"next":     "cd " + filepath.Join(repo, task.Worktree) + " && continue; maw submit",
		}, nil
	}
	if dryRun {
		return Result{"ok": true, "dry_run": true, "task": task}, nil
	}
	_, _ = refreshBoard(h)
	wt := filepath.Join(repo, wtRel)
	return Result{
		"ok": true, "task": task, "worktree": wt, "branch": branch,
		"env":  map[string]string{"MAW_AGENT": who, "MAW_TASK": task.ID},
		"next": "cd " + wt + " && implement within write_scope; maw progress -m '...'; maw submit",
	}, nil
}

func Progress(cwd, message, taskID string) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	if taskID == "" {
		taskID, err = inferTaskID(cwd)
		if err != nil {
			return nil, err
		}
	}
	var hb string
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, taskID)
		if err != nil || t == nil {
			return errx.New("unknown task "+taskID, "maw task list")
		}
		if err := identity.RequireOwner(who, t); err != nil {
			return err
		}
		t.Heartbeat = db.UTCNow()
		t.Revision++
		hb = t.Heartbeat
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		return db.AppendEvent(tx, who, "progress", taskID, map[string]any{"message": message})
	})
	if err != nil {
		return nil, err
	}
	return Result{"ok": true, "task_id": taskID, "heartbeat": hb, "next": "continue or `maw submit`"}, nil
}

func filterScopeFiles(files []string) []string {
	var out []string
	for _, f := range files {
		if f == "TASK.md" || strings.HasPrefix(f, ".maw/") {
			continue
		}
		out = append(out, f)
	}
	return out
}

func Submit(cwd, taskID string, dryRun bool) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(home.ConfigPath(h))
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	if taskID == "" {
		taskID, err = inferTaskID(cwd)
		if err != nil {
			return nil, err
		}
	}
	wt, err := home.WorktreeRoot(cwd)
	if err != nil {
		wt, _ = home.RepoRoot(cwd)
	}
	files, err := gitops.ChangedFiles(wt, cfg.Project.MainBranch)
	if err != nil {
		return nil, err
	}
	files = filterScopeFiles(files)
	var status string
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, taskID)
		if err != nil || t == nil {
			return errx.New("unknown task "+taskID, "maw task list")
		}
		if err := identity.RequireOwner(who, t); err != nil {
			return err
		}
		if t.Status != "in_progress" && t.Status != "self_check" && t.Status != "conflict" {
			return errx.New("task "+taskID+" status "+t.Status+" cannot submit", "only in_progress tasks can be submitted")
		}
		if err := scope.AssertFilesInScope(files, t.WriteScope, taskID); err != nil {
			return err
		}
		if dryRun {
			status = t.Status
			return nil
		}
		if err := machine.AssertTransition(t.Status, "in_review"); err != nil {
			return err
		}
		t.Status = "in_review"
		t.Heartbeat = db.UTCNow()
		t.Revision++
		_ = db.AppendEvent(tx, who, "status_changed", taskID, map[string]any{"to": "in_review", "files": files})
		if cfg.Approvals.Review == "auto" {
			_ = machine.AssertTransition("in_review", "merging")
			t.Status = "merging"
			t.Revision++
			_ = db.AppendEvent(tx, "system", "review_auto_approved", taskID, nil)
		}
		status = t.Status
		return db.UpsertTask(tx, t)
	})
	if err != nil {
		return nil, err
	}
	if dryRun {
		return Result{"ok": true, "dry_run": true, "files": files}, nil
	}
	_, _ = refreshBoard(h)
	next := "export MAW_AGENT=int && maw merge --yes"
	if cfg.Approvals.Review != "auto" {
		next = "maw review --approve " + taskID
	}
	return Result{"ok": true, "task_id": taskID, "files": files, "status": status, "next": next}, nil
}

func Review(cwd, taskID string, approve bool, message string) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	var status string
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, taskID)
		if err != nil || t == nil {
			return errx.New("unknown task "+taskID, "")
		}
		if t.Owner == who {
			return errx.New("author cannot review own task", "use another MAW_AGENT")
		}
		if t.Status != "in_review" {
			return errx.New("task not in_review ("+t.Status+")", "")
		}
		if approve {
			_ = machine.AssertTransition("in_review", "merging")
			t.Status = "merging"
			_ = db.AppendEvent(tx, who, "review_result", taskID, map[string]any{"result": "approve", "message": message})
		} else {
			_ = machine.AssertTransition("in_review", "in_progress")
			t.Status = "in_progress"
			_ = db.AppendEvent(tx, who, "review_result", taskID, map[string]any{"result": "request_changes", "message": message})
		}
		t.Revision++
		status = t.Status
		return db.UpsertTask(tx, t)
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(h)
	next := "author: fix and maw submit"
	if approve {
		next = "maw merge --yes"
	}
	return Result{"ok": true, "task_id": taskID, "status": status, "next": next}, nil
}

func Merge(cwd string, yes, dryRun bool) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	repo, err := home.RepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(home.ConfigPath(h))
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	if err := identity.RequireIntegrator(who, cfg); err != nil {
		return nil, err
	}
	if cfg.Approvals.Merge == "manual" && !yes {
		return nil, errx.New("merge requires confirmation", "run `maw merge --yes`")
	}
	var queue []*db.Task
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		tasks, err := db.ListTasks(tx)
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.Status == "merging" {
				queue = append(queue, t)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(queue) == 0 {
		return nil, errx.New("merge queue empty", "wait for submit/review")
	}
	// sort by merge_order
	for i := 0; i < len(queue); i++ {
		for j := i + 1; j < len(queue); j++ {
			if queue[j].MergeOrder < queue[i].MergeOrder || (queue[j].MergeOrder == queue[i].MergeOrder && queue[j].ID < queue[i].ID) {
				queue[i], queue[j] = queue[j], queue[i]
			}
		}
	}
	if dryRun {
		ids := make([]string, len(queue))
		for i, t := range queue {
			ids[i] = t.ID
		}
		return Result{"ok": true, "dry_run": true, "queue": ids}, nil
	}
	var results []map[string]any
	for _, task := range queue {
		tid := task.ID
		wt := filepath.Join(repo, task.Worktree)
		mergeErr := func() error {
			if st, err := os.Stat(wt); err == nil && st.IsDir() {
				if err := gitops.RebaseOntoMain(wt, cfg.Project.MainBranch); err != nil {
					return err
				}
			}
			gateCwd := repo
			if st, err := os.Stat(wt); err == nil && st.IsDir() {
				gateCwd = wt
			}
			if err := gitops.RunGates(cfg.Gates.Commands, gateCwd, cfg.Gates.TimeoutSec); err != nil {
				return err
			}
			if cur, _ := gitops.CurrentBranch(repo); cur != cfg.Project.MainBranch {
				_, _ = home.Git(repo, "checkout", cfg.Project.MainBranch)
			}
			return gitops.SquashMerge(repo, task.Branch, cfg.Project.MainBranch, "maw: merge "+tid+" "+task.Title)
		}()
		if mergeErr != nil {
			msg := mergeErr.Error()
			if e, ok := mergeErr.(*errx.Error); ok {
				msg = e.Msg
			}
			_ = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
				t, _ := db.GetTask(tx, tid)
				if t == nil {
					return nil
				}
				t.Status = "conflict"
				t.BlockedReason = msg
				if len(t.BlockedReason) > 500 {
					t.BlockedReason = t.BlockedReason[:500]
				}
				t.Attempts++
				t.Revision++
				_ = db.UpsertTask(tx, t)
				_ = db.AppendEvent(tx, who, "gate_failed", tid, map[string]any{"error": msg})
				note := filepath.Join(h, "handoffs", tid+"-conflict.md")
				_ = os.WriteFile(note, []byte("# Conflict: "+tid+"\n\n"+msg+"\n"), 0o644)
				return nil
			})
			results = append(results, map[string]any{"id": tid, "status": "conflict", "error": msg})
			break
		}
		_ = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
			t, _ := db.GetTask(tx, tid)
			if t == nil {
				return nil
			}
			_ = machine.AssertTransition(t.Status, "done")
			t.Status = "done"
			t.Revision++
			_ = db.UpsertTask(tx, t)
			_ = db.AppendEvent(tx, who, "merged", tid, nil)
			return promoteReady(tx)
		})
		results = append(results, map[string]any{"id": tid, "status": "done"})
	}
	html, _ := refreshBoard(h)
	allOK := true
	for _, r := range results {
		if r["status"] != "done" {
			allOK = false
		}
	}
	return Result{"ok": allOK, "results": results, "board": html, "next": "maw board"}, nil
}

func Board(cwd string) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(home.ConfigPath(h))
	if err != nil {
		return nil, err
	}
	var tasks []*db.Task
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		var e error
		tasks, e = db.ListTasks(tx)
		return e
	})
	if err != nil {
		return nil, err
	}
	htmlPath, err := board.Write(h, tasks, cfg.Project.MaxParallel)
	if err != nil {
		return nil, err
	}
	term := board.RenderTerminal(tasks, cfg.Project.MaxParallel)
	counts := map[string]int{}
	for _, t := range tasks {
		counts[t.Status]++
	}
	return Result{"ok": true, "terminal": term, "html": htmlPath, "counts": counts, "next": "open HTML board or run workers / merge"}, nil
}

func TaskList(cwd string) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	var tasks []*db.Task
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		var e error
		tasks, e = db.ListTasks(tx)
		return e
	})
	return Result{"ok": true, "tasks": tasks}, err
}

func TaskShow(cwd, id string) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	var t *db.Task
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		var e error
		t, e = db.GetTask(tx, id)
		return e
	})
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errx.New("unknown task "+id, "maw task list")
	}
	return Result{"ok": true, "task": t}, nil
}

func Block(cwd, message, taskID string) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	if taskID == "" {
		taskID, err = inferTaskID(cwd)
		if err != nil {
			return nil, err
		}
	}
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, taskID)
		if err != nil || t == nil {
			return errx.New("unknown task "+taskID, "")
		}
		if err := identity.RequireOwner(who, t); err != nil {
			return err
		}
		t.PrevStatus = t.Status
		t.Status = "blocked"
		t.BlockedReason = message
		t.Revision++
		_ = db.AppendEvent(tx, who, "status_changed", taskID, map[string]any{"to": "blocked", "reason": message})
		return db.UpsertTask(tx, t)
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(h)
	return Result{"ok": true, "task_id": taskID, "next": "maw task unblock " + taskID}, nil
}

func Unblock(cwd, taskID string) (Result, error) {
	h, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	var prev string
	err = db.WithTx(home.DBPath(h), func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, taskID)
		if err != nil || t == nil {
			return errx.New("unknown task "+taskID, "")
		}
		prev = t.PrevStatus
		if prev == "" {
			prev = "ready"
		}
		t.Status = prev
		t.BlockedReason = ""
		t.PrevStatus = ""
		t.Revision++
		_ = db.AppendEvent(tx, "orchestrator", "status_changed", taskID, map[string]any{"to": prev})
		return db.UpsertTask(tx, t)
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(h)
	return Result{"ok": true, "task_id": taskID, "status": prev}, nil
}

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
				if br, _ := gitops.CurrentBranch(root); br == cfg.Project.MainBranch {
					return nil, errx.New("commits on main branch are blocked by maw hook", "run `maw next` and work in the task worktree")
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
			return nil, errx.New("pre-push: refusing to push "+remoteRef, "only maw merge updates main; push agent/* branches only")
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
