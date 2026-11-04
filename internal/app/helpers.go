package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/machine"
	"github.com/kiwh77/maw/internal/schedule"
)

type Result map[string]any

func refreshBoard(pc *projCtx) (string, error) {
	snap, err := loadBoardSnapshot(pc.GlobalDB, "", "", "", pc.Project.MaxParallel)
	if err != nil {
		return "", err
	}
	if gdir, err := home.GlobalDir(); err == nil {
		_, _ = board.Write(gdir, snap)
	}
	return board.Write(pc.LocalHome, snap)
}

// mergeAgentMeta copies existing meta and optionally sets log_path.
// Empty logPath leaves any prior log_path untouched.
func mergeAgentMeta(existing map[string]any, logPath string) map[string]any {
	meta := map[string]any{}
	if existing != nil {
		for k, v := range existing {
			meta[k] = v
		}
	}
	if strings.TrimSpace(logPath) != "" {
		meta["log_path"] = strings.TrimSpace(logPath)
	}
	return meta
}

func bindAgent(tx *sql.Tx, projectID, who, role, status, taskID, worktree, branch, logPath string) error {
	if role == "" {
		role = "worker"
	}
	existing, _ := db.GetAgent(tx, projectID, who)
	regAt := db.UTCNow()
	if existing != nil && existing.RegisteredAt != "" {
		regAt = existing.RegisteredAt
	}
	var prevMeta map[string]any
	if existing != nil {
		prevMeta = existing.Meta
	}
	return db.UpsertAgent(tx, &db.Agent{
		ProjectID: projectID, ID: who, Role: role, Status: status,
		TaskID: taskID, Worktree: worktree, Branch: branch,
		RegisteredAt: regAt, LastSeen: db.UTCNow(),
		Meta: mergeAgentMeta(prevMeta, logPath),
	})
}

func writeTaskCard(wt string, t *db.Task, agent string) error {
	return writeTaskCardWithDeps(wt, t, agent, nil)
}

func writeTaskCardWithDeps(wt string, t *db.Task, agent string, all []*db.Task) error {
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

	goal := extraString(t, "goal")
	if goal == "" {
		goal = extractPrefixedAcceptance(t.Acceptance, "现状：")
	}
	if goal == "" {
		goal = "（蓝图未写 goal；见 acceptance / read_scope）"
	}

	var accLines []string
	for _, a := range t.Acceptance {
		if strings.HasPrefix(a, "现状：") || strings.HasPrefix(a, "不要：") {
			continue
		}
		accLines = append(accLines, "- [ ] "+a)
	}
	if len(accLines) == 0 {
		accLines = []string{"- [ ] （无）"}
	}

	verify := extraStringSlice(t, "verify")
	if len(verify) == 0 {
		verify = extractPrefixedList(t.Acceptance, "Verify:")
	}
	var verifyLines []string
	for _, v := range verify {
		verifyLines = append(verifyLines, "- `"+v+"`")
	}
	if len(verifyLines) == 0 {
		verifyLines = []string{"- （无；请在蓝图补 verify）"}
	}

	var readLines []string
	for _, s := range t.ReadScope {
		readLines = append(readLines, "- `"+s+"`")
	}
	if len(readLines) == 0 {
		readLines = []string{"- （无）"}
	}

	byID := map[string]*db.Task{}
	for _, o := range all {
		if o != nil {
			byID[o.ID] = o
		}
	}
	var depLines []string
	for _, d := range t.Deps {
		if up := byID[d]; up != nil {
			acc := ""
			if len(up.Acceptance) > 0 {
				acc = up.Acceptance[0]
			}
			depLines = append(depLines, fmt.Sprintf("- %s %s —— 验收：%s", up.ID, up.Title, acc))
		} else {
			depLines = append(depLines, "- "+d)
		}
	}
	if len(depLines) == 0 {
		depLines = []string{"- （无上游）"}
	}

	var scopeLines []string
	for _, s := range t.WriteScope {
		scopeLines = append(scopeLines, "- `"+s+"`")
	}

	notDoing := extraStringSlice(t, "not_doing")
	if len(notDoing) == 0 {
		notDoing = extractPrefixedList(t.Acceptance, "不要：")
	}
	var notLines []string
	for _, n := range notDoing {
		notLines = append(notLines, "- "+n)
	}
	if len(notLines) == 0 {
		notLines = []string{"- （无）"}
	}

	var bld strings.Builder
	fmt.Fprintf(&bld, "# %s: %s\n\n", t.ID, t.Title)
	fmt.Fprintf(&bld, "## Goal\n%s\n\n", goal)
	fmt.Fprintf(&bld, "## 完成判据\n%s\n\n", strings.Join(accLines, "\n"))
	fmt.Fprintf(&bld, "## 验证命令\n%s\n\n", strings.Join(verifyLines, "\n"))
	fmt.Fprintf(&bld, "## 先读这些（read_scope）\n%s\n\n", strings.Join(readLines, "\n"))
	fmt.Fprintf(&bld, "## 上游已交付（deps）\n%s\n\n", strings.Join(depLines, "\n"))
	fmt.Fprintf(&bld, "## 可写范围（write_scope）\n%s\n\n", strings.Join(scopeLines, "\n"))
	fmt.Fprintf(&bld, "## 不要做（not_doing）\n%s\n\n", strings.Join(notLines, "\n"))
	fmt.Fprintf(&bld, "## 协议\n负责人 `%s`；工程 `%s`。\n", agent, t.ProjectID)
	bld.WriteString("里程碑 `maw progress -m \"...\"`；完成 `maw submit`；随后可 `maw merge --yes`。\n")
	return os.WriteFile(filepath.Join(wt, "TASK.md"), []byte(bld.String()), 0o644)
}

func extraString(t *db.Task, key string) string {
	if t == nil || t.Extra == nil {
		return ""
	}
	if v, ok := t.Extra[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func extraStringSlice(t *db.Task, key string) []string {
	if t == nil || t.Extra == nil {
		return nil
	}
	raw, ok := t.Extra[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}

func extractPrefixedAcceptance(acc []string, prefix string) string {
	for _, a := range acc {
		if strings.HasPrefix(a, prefix) {
			return a
		}
	}
	return ""
}

func extractPrefixedList(acc []string, prefix string) []string {
	var out []string
	for _, a := range acc {
		if strings.HasPrefix(a, prefix) {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(a, prefix)))
		}
	}
	return out
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
		return "", i18n.E("err.infer_task", "next.cd_or_task")
	}
	var t db.Task
	if err := json.Unmarshal(b, &t); err != nil || t.ID == "" {
		return "", i18n.E("err.bad_task_json", "next.rerun_next")
	}
	return t.ID, nil
}

func promoteReady(tx *sql.Tx, projectID string) error {
	shared, err := db.ListSharedFiles(tx, projectID)
	if err != nil {
		return err
	}
	for {
		tasks, err := db.ListTasks(tx, projectID)
		if err != nil {
			return err
		}
		changed := false
		for _, t := range tasks {
			if t.Status != "backlog" && t.Status != "ready" {
				continue
			}
			ok := schedule.CanActivateShared(t, tasks, shared)
			if ok && t.Status != "ready" {
				from := t.Status
				if err := machine.AssertTransition(t.Status, "ready"); err != nil {
					return err
				}
				t.Status = "ready"
				t.Revision++
				if err := db.UpsertTask(tx, t); err != nil {
					return err
				}
				_ = db.AppendEvent(tx, projectID, "orchestrator", "status_changed", t.ID, map[string]any{
					"from": from, "to": "ready",
				})
				changed = true
				break
			}
			if !ok && t.Status == "ready" {
				from := t.Status
				if err := machine.AssertTransition(t.Status, "backlog"); err != nil {
					return err
				}
				t.Status = "backlog"
				t.Revision++
				if err := db.UpsertTask(tx, t); err != nil {
					return err
				}
				_ = db.AppendEvent(tx, projectID, "orchestrator", "status_changed", t.ID, map[string]any{
					"from": from, "to": "backlog",
				})
				changed = true
				break
			}
		}
		if !changed {
			return nil
		}
	}
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

func loadBoardSnapshot(gdb, projectID, agent, status string, maxParallelFallback int) (board.Snapshot, error) {
	var tasks []*db.Task
	var agents []*db.Agent
	var projects []*db.Project
	var iterations []*db.Iteration
	var eventID int64
	var lastEvent string
	maxP := maxParallelFallback
	if maxP <= 0 {
		maxP = 4
	}
	// 看板查询是只读：不得隐式升级 schema。
	err := withGlobalRead(gdb, func(tx *sql.Tx) error {
		var e error
		projects, e = db.ListProjects(tx)
		if e != nil {
			return e
		}
		tasks, e = db.ListTasksFiltered(tx, projectID, agent, status)
		if e != nil {
			return e
		}
		agents, e = db.ListAgentsFiltered(tx, projectID, agent)
		if e != nil {
			return e
		}
		iterations, e = db.ListIterations(tx, projectID)
		if e != nil {
			return e
		}
		// 数据水位取「最近一条事件发生时间」：它不是渲染时刻，停更时不会假装在更新。
		if projectID == "" {
			return tx.QueryRow(`SELECT COALESCE(MAX(id), 0), COALESCE(MAX(ts), '') FROM events`).
				Scan(&eventID, &lastEvent)
		}
		return tx.QueryRow(`SELECT COALESCE(MAX(id), 0), COALESCE(MAX(ts), '') FROM events WHERE project_id = ?`, projectID).
			Scan(&eventID, &lastEvent)
	})
	if err != nil {
		return board.Snapshot{}, err
	}
	if projectID != "" {
		for _, p := range projects {
			if p.ID == projectID && p.MaxParallel > 0 {
				maxP = p.MaxParallel
			}
		}
	}
	snap := board.BuildSnapshot(tasks, agents, maxP)
	snap.EventID = eventID
	// 没有任何事件时留空：让前端显示「尚无事件」，而不是编一个时间。
	if ts, perr := time.Parse(time.RFC3339, strings.TrimSpace(lastEvent)); perr == nil {
		snap.UpdatedAt = ts.Local().Format("2006-01-02 15:04:05")
	} else {
		snap.UpdatedAt = ""
	}
	snap.Projects = projects
	if snap.Projects == nil {
		snap.Projects = []*db.Project{}
	}
	snap.Iterations = iterations
	if snap.Iterations == nil {
		snap.Iterations = []*db.Iteration{}
	}
	snap.FilterProject = projectID
	snap.FilterAgent = agent
	snap.FilterStatus = status
	return snap, nil
}

func loadTaskDetail(gdb, projectID, taskID string) (board.TaskDetail, error) {
	var out board.TaskDetail
	err := withGlobalRead(gdb, func(tx *sql.Tx) error {
		t, e := db.FindTask(tx, projectID, taskID)
		if e != nil {
			return e
		}
		out.Task = t
		if t == nil {
			return nil
		}
		pid := projectID
		if pid == "" {
			pid = t.ProjectID
		}
		if pid != "" {
			if p, e2 := db.GetProject(tx, pid); e2 == nil {
				out.Project = p
			}
		}
		ev, e := db.ListEventsByTask(tx, t.ProjectID, t.ID, 200)
		if e != nil {
			return e
		}
		out.Events = ev
		return nil
	})
	return out, err
}

func agentIDs(agents []*db.Agent) []string {
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		if a != nil && a.ID != "" {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

// workerNameWarn 对非规范 Worker 名给出建议（integrator/reviewer 豁免）。
func workerNameWarn(who string, pc *projCtx) string {
	if who == "" || pc == nil || pc.Config == nil {
		return ""
	}
	if r := identity.Role(who, pc.Config); r == "integrator" || r == "reviewer" {
		return ""
	}
	if identity.IsCanonical(who) {
		return ""
	}
	return "Worker 名建议用 {应用}-{工程}-{序号}，例如 " + identity.Suggest(pc.Project.Name, nil) + "；当前名仍可用"
}

func attachAgentNameMeta(res Result, who string, allocated bool, warn string) {
	res["agent_name_format"] = "{app}-{project}-{n}"
	if allocated {
		res["agent_allocated"] = true
	}
	if warn != "" {
		res["agent_name_warning"] = warn
	}
	if who != "" {
		res["agent_name_canonical"] = identity.IsCanonical(who)
	}
}
