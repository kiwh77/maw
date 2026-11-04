package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/home"
	httpresources "github.com/kiwh77/maw/internal/httpapi/resources"
)

// ProjectRemovalOptions 控制一次工程移除。
type ProjectRemovalOptions struct {
	// Force 允许在有活态占位时仍然移除；默认拒绝，保护正在跑的现场。
	Force bool
	// DryRun 只回报将删的行数，不写库。
	DryRun bool
	// Actor 写进审计事件的操作者；空串记为 system。
	Actor string
}

// AgentAliveWindow 是判定 Agent「仍在线」的心跳窗口。
//
// 与 maw 的租约判活一致：心跳过了窗口就不再算活人，否则一个异常退出的
// Agent 会让它的工程永远删不掉。
const AgentAliveWindow = 15 * time.Minute

// RemoveProject 把工程从本机中枢移除：注销登记、清活态数据、留审计。
//
// ref 可以是工程 id，也可以是工程名（看板与 CLI 都这么用）。
// 保留三类台账：任务实例与冻结版本、事件流水、用量记录——它们是历史，
// 不是工程的活态状态；清掉它们等于改写发生过的事。
func RemoveProject(cwd, ref string, opts ProjectRemovalOptions) (Result, error) {
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	if err := ensureWritableDB(gdb); err != nil {
		return nil, err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errx.New("需要指定工程（id 或名称）",
			"先运行 maw project list 看已登记的工程")
	}

	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = "system"
	}
	aliveSince := time.Now().UTC().Add(-AgentAliveWindow).Format("2006-01-02T15:04:05Z")

	var res Result
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		project, err := resolveProjectRef(tx, ref)
		if err != nil {
			return err
		}
		tasks, agents, err := db.ProjectRemovalEvidence(tx, project.ID, aliveSince)
		if err != nil {
			return err
		}
		busy := len(tasks) > 0 || len(agents) > 0
		// 预览不被现场拦住：只读地看清「会删什么、会被什么拦住」才有意义。
		if busy && !opts.Force && !opts.DryRun {
			return &db.ProjectBusyError{Tasks: tasks, Agents: agents}
		}

		instances, versions, events, usage, err := db.RetainedLedgerCounts(tx, project.ID)
		if err != nil {
			return err
		}
		retained := map[string]int64{
			"task_instances": instances,
			"task_versions":  versions,
			"events":         events,
			"usage_records":  usage,
		}

		if opts.DryRun {
			// 预览不能报「已删」：行数只能给「将删」的现场证据。
			would, err := previewPurgeCounts(tx, project.ID)
			if err != nil {
				return err
			}
			res = Result{
				"ok": true, "dry_run": true, "blocked": busy, "project_id": project.ID,
				"project_name": project.Name, "project_path": project.Path,
				"would_remove": purgeCountsMap(would), "retained": retained,
				"active_tasks": tasks, "online_agents": agents,
				"summary": removalSummary(project.Name, would, events, versions, true),
				"next":    dryRunNext(busy, opts.Force),
			}
			return nil
		}

		counts, err := db.PurgeProject(tx, project.ID)
		if err != nil {
			return err
		}
		removed := purgeCountsMap(counts)
		if err := db.AppendEvent(tx, "", actor, "project_removed", "", map[string]any{
			"project_id":   project.ID,
			"project_name": project.Name,
			"project_path": project.Path,
			"forced":       opts.Force,
			"removed":      removed,
			"retained":     map[string]int64{"events": events, "task_versions": versions},
		}); err != nil {
			return err
		}

		res = Result{
			"ok": true, "dry_run": false, "forced": opts.Force,
			"project_id": project.ID, "project_name": project.Name, "project_path": project.Path,
			"removed": removed, "retained": retained,
			"summary": removalSummary(project.Name, counts, events, versions, false),
			"next":    "工程已从中枢移除；工程目录本身没动，需要时可在该目录重新 maw init",
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, db.ErrProjectBusy) {
			return nil, busyError(err)
		}
		return nil, err
	}
	return res, nil
}

// ProjectList 列出本机中枢里登记的全部工程，供 CLI 与「哪些该移除」对照。
func ProjectList(cwd string) (Result, error) {
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	var projects []*db.Project
	if err := withGlobalRead(gdb, func(tx *sql.Tx) error {
		var e error
		projects, e = db.ListProjects(tx)
		return e
	}); err != nil {
		return nil, err
	}
	items := make([]Result, 0, len(projects))
	for _, p := range projects {
		items = append(items, Result{
			"project_id": p.ID, "project_name": p.Name, "project_path": p.Path,
			"last_seen": p.LastSeen,
		})
	}
	return Result{
		"ok": true, "count": len(items), "projects": items,
		"next": "maw project rm <id|名称> 移除已废弃的登记（该工程仍在跑任务时会被拦住）",
	}, nil
}

// resolveProjectRef 先按 id 找，再按名称／目录名找；同名多义时不猜。
func resolveProjectRef(tx *sql.Tx, ref string) (*db.Project, error) {
	if p, err := db.GetProject(tx, ref); err != nil {
		return nil, err
	} else if p != nil {
		return p, nil
	}
	projects, err := db.ListProjects(tx)
	if err != nil {
		return nil, err
	}
	var matches []*db.Project
	for _, p := range projects {
		if p.Name == ref || filepath.Base(p.Path) == ref {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return nil, &projectNotFoundError{ref: ref}
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		return nil, &ambiguousProjectError{ref: ref, ids: ids}
	}
}

// busyRemovalError 是「现场仍有活态占位」的拒绝原因。
//
// 它保留 Unwrap 链：CLI 只要人能读的下一步，HTTP 层与测试则要能
// errors.Is(err, db.ErrProjectBusy) 判定成 409，两者不能互相牺牲。
type busyRemovalError struct {
	msg   string
	inner error
	next  string
}

func (e *busyRemovalError) Error() string {
	if e.next == "" {
		return e.msg
	}
	return e.msg + "\nNext: " + e.next
}

func (e *busyRemovalError) Unwrap() error { return e.inner }

// busyError 把现场证据翻成可执行的下一步说明。
func busyError(err error) error {
	var busy *db.ProjectBusyError
	if !errors.As(err, &busy) {
		return err
	}
	lines := make([]string, 0, 4)
	for i, task := range busy.Tasks {
		if i == 3 {
			lines = append(lines, fmt.Sprintf("…另有 %d 条未完成任务", len(busy.Tasks)-3))
			break
		}
		lines = append(lines, fmt.Sprintf("任务 %s（%s，持有者 %s）", task.ID, task.Status, task.Owner))
	}
	for i, agent := range busy.Agents {
		if i == 2 {
			break
		}
		lines = append(lines, fmt.Sprintf("Agent %s（%s，最后心跳 %s）", agent.ID, agent.Status, agent.LastSeen))
	}
	return &busyRemovalError{
		msg:   "工程仍有活态占位，拒绝移除：" + strings.Join(lines, "；"),
		inner: err,
		next:  "先让这些任务收尾（submit／block）或等 Agent 下线；确认要连现场一起清掉时加 --force",
	}
}

// previewPurgeCounts 只读地算出「如果删会删掉多少行」。
//
// 字段与 PurgeProject 的返回逐项对齐：预览少报一张表，等于让用户按错的规模做决定。
func previewPurgeCounts(tx *sql.Tx, projectID string) (db.ProjectPurgeCounts, error) {
	var c db.ProjectPurgeCounts
	queries := []struct {
		dst   *int64
		query string
	}{
		{&c.Tasks, `SELECT COUNT(*) FROM tasks WHERE project_id = ?`},
		{&c.Agents, `SELECT COUNT(*) FROM agents WHERE project_id = ?`},
		{&c.Locks, `SELECT COUNT(*) FROM locks WHERE project_id = ?`},
		{&c.Messages, `SELECT COUNT(*) FROM messages WHERE project_id = ?`},
		{&c.SharedFiles, `SELECT COUNT(*) FROM shared_files WHERE project_id = ?`},
		{&c.Nodes, `SELECT COUNT(*) FROM nodes WHERE project_id = ?`},
		{&c.Leases, `SELECT COUNT(*) FROM resource_leases WHERE project_id = ?`},
		{&c.Runs, `SELECT COUNT(*) FROM runs WHERE project_id = ?`},
		{&c.RunEvents, `SELECT COUNT(*) FROM run_events WHERE project_id = ?`},
		{&c.Operations, `SELECT COUNT(*) FROM operations WHERE project_id = ?`},
		{&c.Deliveries, `SELECT COUNT(*) FROM deliveries WHERE project_id = ?`},
		{&c.DeliveryVerifications, `SELECT COUNT(*) FROM delivery_verifications WHERE delivery_id IN
			(SELECT delivery_id FROM deliveries WHERE project_id = ?)`},
		{&c.DeliveryReviews, `SELECT COUNT(*) FROM delivery_reviews WHERE delivery_id IN
			(SELECT delivery_id FROM deliveries WHERE project_id = ?)`},
		{&c.SourceSessions, `SELECT COUNT(*) FROM source_sessions WHERE project_id = ?`},
		{&c.SourceTurns, `SELECT COUNT(*) FROM source_turns WHERE session_id IN
			(SELECT session_id FROM source_sessions WHERE project_id = ?)`},
		{&c.SourceLinks, `SELECT COUNT(*) FROM run_source_links WHERE project_id = ?`},
		{&c.ProjectionStates, `SELECT COUNT(*) FROM projection_state WHERE project_id = ?`},
		{&c.Notifications, `SELECT COUNT(*) FROM notification_deliveries WHERE project_id = ?`},
	}
	for _, q := range queries {
		if err := tx.QueryRow(q.query, projectID).Scan(q.dst); err != nil {
			return db.ProjectPurgeCounts{}, err
		}
	}
	// 工程登记本身：能走到这里的 ref 一定存在，删掉就是 1 行。
	c.Projects = 1
	return c, nil
}

// projectRemovalPort 把 app 用例接到 /api/v1 的写资源端口上。
//
// 端口在 httpapi/resources，用例在 app：中间这层只做错误映射与字段搬运，
// 不让 HTTP 包认识 db，也不让用例知道状态码。
type projectRemovalPort struct{ cwd string }

func (p projectRemovalPort) Remove(ctx context.Context, req httpresources.ProjectRemovalRequest) (httpresources.ProjectRemovalResult, error) {
	res, err := RemoveProject(p.cwd, req.ProjectID, ProjectRemovalOptions{
		Force: req.Force, Actor: req.Actor,
	})
	if err != nil {
		return httpresources.ProjectRemovalResult{}, mapProjectRemovalErr(err)
	}
	return httpresources.ProjectRemovalResult{
		ProjectID:   resultString(res, "project_id"),
		ProjectName: resultString(res, "project_name"),
		ProjectPath: resultString(res, "project_path"),
		Summary:     resultString(res, "summary"),
		Removed:     resultIntMap(res, "removed"),
		Retained:    resultIntMap(res, "retained"),
	}, nil
}

func (p projectRemovalPort) Update(ctx context.Context, req httpresources.ProjectUpdateRequest) (httpresources.ProjectUpdateResult, error) {
	_ = ctx
	res, err := ProjectUpdate(p.cwd, ProjectUpdateInput{
		Ref: req.ProjectID, Name: req.Name, Path: req.Path,
		MainBranch: req.MainBranch, MaxParallel: req.MaxParallel,
		AutoEnabled: req.AutoEnabled, Limit: req.Limit,
		AllowedNodes: req.AllowedNodes, WorkerNode: req.WorkerNode,
		LLMNode: req.LLMNode,
	})
	if err != nil {
		return httpresources.ProjectUpdateResult{}, mapProjectRemovalErr(err)
	}
	written, _ := res["config_written"].(bool)
	maxP, _ := res["max_parallel"].(int)
	return httpresources.ProjectUpdateResult{
		ProjectID: resultString(res, "project_id"), ProjectName: resultString(res, "project_name"),
		ProjectPath: resultString(res, "project_path"), MainBranch: resultString(res, "main_branch"),
		MaxParallel: maxP, ConfigPath: resultString(res, "config_path"), ConfigWriten: written,
	}, nil
}

func resultString(res Result, key string) string {
	if v, ok := res[key].(string); ok {
		return v
	}
	return ""
}

func resultIntMap(res Result, key string) map[string]int64 {
	if v, ok := res[key].(map[string]int64); ok {
		return v
	}
	return nil
}

// mapProjectRemovalErr 把用例错误翻成端口错误：HTTP 状态只在这里定一次。
func mapProjectRemovalErr(err error) error {
	switch {
	case errors.Is(err, db.ErrProjectNotFound):
		return httpresources.ErrNotFound{Message: "工程未登记"}
	case errors.Is(err, db.ErrProjectBusy):
		details := map[string]any{}
		var busy *db.ProjectBusyError
		if errors.As(err, &busy) {
			details["tasks"] = busy.Tasks
			details["agents"] = busy.Agents
		}
		return httpresources.ErrConflict{
			Code:    "project_busy",
			Message: "工程仍有未完成的任务或在线 Agent，移除被拒绝",
			Details: details,
		}
	case errors.Is(err, ErrAmbiguousProjectRef):
		return httpresources.ErrConflict{
			Code:    "ambiguous_project",
			Message: "该名称对应多个工程，请改用工程 id",
		}
	}
	return err
}

// ErrAmbiguousProjectRef 表示按名称移除时命中了多个工程。
var ErrAmbiguousProjectRef = errors.New("app: 工程名称不唯一")

// projectNotFoundError 让「按名称没找到」也能被 errors.Is 判定成未登记。
type projectNotFoundError struct{ ref string }

func (e *projectNotFoundError) Error() string {
	return fmt.Sprintf("中枢里没有登记 %q\nNext: 运行 maw project list 核对工程 id 与名称", e.ref)
}

func (e *projectNotFoundError) Is(target error) bool { return target == db.ErrProjectNotFound }

// ambiguousProjectError 带上是哪几个 id，避免用户重试同一个歧义名称。
type ambiguousProjectError struct {
	ref string
	ids []string
}

func (e *ambiguousProjectError) Error() string {
	return fmt.Sprintf("%q 对应多个工程：%s\nNext: 改用工程 id 指定，避免删错",
		e.ref, strings.Join(e.ids, ", "))
}

func (e *ambiguousProjectError) Is(target error) bool { return target == ErrAmbiguousProjectRef }

// removalSummary 是一行人能读懂的「删了什么／留了什么」。
//
// 只列非零项：全零的清单看着像报错，反而要读者自己数。
func removalSummary(name string, c db.ProjectPurgeCounts, events, versions int64, preview bool) string {
	verb := "移除"
	if preview {
		verb = "将移除"
	}
	parts := make([]string, 0, 6)
	add := func(label string, n int64) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", label, n))
		}
	}
	add("任务", c.Tasks)
	add("Agent", c.Agents)
	add("锁", c.Locks)
	add("消息", c.Messages)
	add("共享文件", c.SharedFiles)
	add("运行", c.Runs)
	add("操作", c.Operations)
	add("交付", c.Deliveries)
	add("会话", c.SourceSessions)
	if len(parts) == 0 {
		parts = append(parts, "没有关联数据")
	}
	return fmt.Sprintf("%s %s：%s；保留事件 %d 条、冻结版本 %d 个（台账不随工程移除销毁）",
		verb, name, strings.Join(parts, "、"), events, versions)
}

func dryRunNext(busy, force bool) string {
	if busy && !force {
		return "工程还有活态占位；收尾后可去掉 --dry-run 执行，或加 --force 连现场一起清掉"
	}
	return "确认无误后去掉 --dry-run 再执行一次"
}

// purgeCountsMap 把删除计数摊平成 map：CLI 的 --json、HTTP 响应与审计事件
// 都直接吃 map，谁也不该为了一个结构体多写一层转码。
func purgeCountsMap(c db.ProjectPurgeCounts) map[string]int64 {
	return map[string]int64{
		"tasks":                   c.Tasks,
		"agents":                  c.Agents,
		"locks":                   c.Locks,
		"messages":                c.Messages,
		"shared_files":            c.SharedFiles,
		"nodes":                   c.Nodes,
		"resource_leases":         c.Leases,
		"runs":                    c.Runs,
		"run_events":              c.RunEvents,
		"operations":              c.Operations,
		"deliveries":              c.Deliveries,
		"delivery_verifications":  c.DeliveryVerifications,
		"delivery_reviews":        c.DeliveryReviews,
		"source_sessions":         c.SourceSessions,
		"source_turns":            c.SourceTurns,
		"run_source_links":        c.SourceLinks,
		"projection_state":        c.ProjectionStates,
		"notification_deliveries": c.Notifications,
		"projects":                c.Projects,
	}
}
