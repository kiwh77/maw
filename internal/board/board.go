package board

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/i18n"
)

// SnapshotTimeLayout 是看板对外时间字段的统一格式（本地时区）。
const SnapshotTimeLayout = "2006-01-02 15:04:05"

// Snapshot 看板数据（供 HTML 轮询 / API）
type Snapshot struct {
	// EventID also invalidates open task logs when only an event was appended.
	EventID int64 `json:"-"`
	// UpdatedAt 是数据水位（最近一次业务数据变更的可观测时间），
	// 不再等于渲染时刻：它只在数据真的变化时才推进。
	UpdatedAt string `json:"updated_at"`
	// GeneratedAt 是本次快照的生成时刻，仅供前端展示「刚刷新」，
	// 不得用来判断数据新鲜度（同一份数据轮询多次，只有它变化）。
	GeneratedAt string   `json:"generated_at"`
	MaxParallel int      `json:"max_parallel"`
	Total       int      `json:"total"`
	Done        int      `json:"done"`
	Active      int      `json:"active"`
	Blocked     int      `json:"blocked"`
	Percent     int      `json:"percent"`
	NextHint    string   `json:"next_hint"`
	Risks       RiskList `json:"risks"`
	// Problems 是聚合出的运行问题（blocked/conflict/重试/停滞/依赖等待），
	// 随本批任务重算，供前端「问题」视图一屏审查。
	Problems ProblemList    `json:"problems"`
	Counts   map[string]int `json:"counts"`
	Tasks    TaskList       `json:"tasks"`
	Agents   AgentList      `json:"agents"`
	// AgentActivity 是 agent id → working|stalled|idle 的展示推导。
	// 纯只读：不写 agents 表、不释放占用，CQ-003 的过期释放语义保持不变。
	AgentActivity map[string]string `json:"agent_activity"`
	Projects      ProjectList       `json:"projects"`
	// Iterations 是筛选下拉里的候选迭代（工程内全部；与当前任务筛选无关）。
	Iterations    IterationList `json:"iterations"`
	FilterProject string        `json:"filter_project"`
	FilterAgent   string        `json:"filter_agent"`
	FilterStatus  string        `json:"filter_status"`
	// FilterIteration 是迭代筛选值：nil 表示没按迭代筛；指向空串表示只筛
	// 「未归属任何迭代」的任务——两者是不同语义，所以不能用一个空字符串表示。
	FilterIteration *string `json:"filter_iteration"`
	// IncludeDone / IncludeArchived 记录当前列表视图放行了什么，
	// 前端据此知道「隐藏了什么」以及怎么显式取回。
	IncludeDone     bool `json:"include_done"`
	IncludeArchived bool `json:"include_archived"`
	Global          bool `json:"global"`
}

// TaskList / AgentList / ProjectList / RiskList 把「nil 集合」固定映射成 JSON []。
// 装配层会在 BuildSnapshot 之后覆盖这些字段（例如空库的工程列表），
// 如果直接用裸切片，nil 会被编码成 null，前端就得为两种空值各写一条分支。
type TaskList []*db.Task

// AgentList 见 TaskList。
type AgentList []*db.Agent

// AgentActivityKind 是看板上 Agent 的三态展示：在推进 / 停滞 / 待命。
//
// 与 db.Agent.Status 不是同一套概念：Status 是权威状态机（working/idle/offline），
// activity 是把它和心跳新鲜度合起来看的**展示推导**，只用于让人一眼分辨
// 「卡住了」和「没活干」。因此绝不反向写回状态、也不触发占用释放。
type AgentActivityKind string

const (
	AgentActivityWorking AgentActivityKind = "working"
	AgentActivityStalled AgentActivityKind = "stalled"
	AgentActivityIdle    AgentActivityKind = "idle"
)

// agentStaleAfter 与 [locks] heartbeat_ttl_sec、project_busy 守卫对齐（默认 30 分钟）。
// 它只是展示阈值：判为 stalled 不会释放任务占用，释放仍由 CQ-003 的
// expireStaleAgents 单独决定（且会按题 heartbeat 续命）。
const agentStaleAfter = 30 * time.Minute

// DeriveAgentActivity 推导单个 Agent 的展示态。纯函数，不改入参。
//
// 判定顺序：
//   - offline：仍占着任务 = stalled，否则 idle；
//   - 非 working 或没有任务：idle；
//   - working + 有任务：心跳在窗口内 = working，过期或时间不可解析 = stalled
//     （证据不足不假装还在推进）。
func DeriveAgentActivity(a *db.Agent, now time.Time) AgentActivityKind {
	if a == nil {
		return AgentActivityIdle
	}
	st := strings.ToLower(strings.TrimSpace(a.Status))
	hasTask := strings.TrimSpace(a.TaskID) != ""
	switch {
	case st == "offline":
		if hasTask {
			return AgentActivityStalled
		}
		return AgentActivityIdle
	case st != "working" || !hasTask:
		return AgentActivityIdle
	}
	last, ok := parseSnapshotTime(a.LastSeen)
	if !ok || now.Sub(last) > agentStaleAfter {
		return AgentActivityStalled
	}
	return AgentActivityWorking
}

// BuildAgentActivity 为一批 Agent 生成 id → activity 的只读映射。
func BuildAgentActivity(agents []*db.Agent, now time.Time) map[string]string {
	out := make(map[string]string, len(agents))
	for _, a := range agents {
		if a == nil || strings.TrimSpace(a.ID) == "" {
			continue
		}
		out[a.ID] = string(DeriveAgentActivity(a, now))
	}
	return out
}

// ProjectList 见 TaskList。
type ProjectList []*db.Project

// IterationList 见 TaskList。
type IterationList []*db.Iteration

// RiskList 见 TaskList。
type RiskList []string

// MarshalJSON 让 nil 列表输出 []，语义等同「这批数据是空的」而不是「这批数据缺失」。
func (l TaskList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]*db.Task(l))
}

// MarshalJSON 见 TaskList。
func (l AgentList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]*db.Agent(l))
}

// MarshalJSON 见 TaskList。
func (l ProjectList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]*db.Project(l))
}

// MarshalJSON 见 TaskList。
func (l IterationList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]*db.Iteration(l))
}

// MarshalJSON 见 TaskList。
func (l RiskList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(l))
}

func byStatus(tasks []*db.Task) map[string][]*db.Task {
	m := map[string][]*db.Task{}
	for _, t := range tasks {
		m[t.Status] = append(m[t.Status], t)
	}
	return m
}

func BuildSnapshot(tasks []*db.Task, agents []*db.Agent, maxParallel int) Snapshot {
	snap := aggregate(tasks, agents, maxParallel)
	snap.Projects = []*db.Project{}
	snap.Global = true
	snap.Normalize()
	return snap
}

// aggregate 只负责「这批任务 + 这些 Agent」的聚合：total/done/percent/risks/next_hint
// 都跟着传进来的任务集走，因此任何按维度收窄过的任务集都可以用它重算。
// 装配层附加的字段（工程列表、筛选回显、数据水位、EventID）不在这里产生。
func aggregate(tasks []*db.Task, agents []*db.Agent, maxParallel int) Snapshot {
	by := byStatus(tasks)
	done := len(by["done"])
	total := len(tasks)
	pct := 0
	if total > 0 {
		pct = 100 * done / total
	}
	active := len(by["claimed"]) + len(by["in_progress"]) + len(by["self_check"]) +
		len(by["in_review"]) + len(by["merging"])
	blocked := len(by["blocked"])
	counts := map[string]int{}
	for _, t := range tasks {
		counts[t.Status]++
	}
	risks := []string{}
	for _, t := range by["blocked"] {
		reason := t.BlockedReason
		if reason == "" {
			reason = "未填写原因"
		}
		prefix := t.ID
		if t.ProjectName != "" {
			prefix = t.ProjectName + "/" + t.ID
		}
		risks = append(risks, fmt.Sprintf("%s 阻塞：%s", prefix, reason))
	}
	for _, t := range by["conflict"] {
		prefix := t.ID
		if t.ProjectName != "" {
			prefix = t.ProjectName + "/" + t.ID
		}
		risks = append(risks, fmt.Sprintf("%s 合并冲突，需作者修复后重新提交", prefix))
	}
	next := "暂无下一步提示"
	switch {
	case len(by["merging"]) > 0:
		next = "有任务待合入：在对应工程执行 maw merge --yes"
	case len(by["in_review"]) > 0:
		next = "有任务待评审：在对应工程执行 maw review --approve <任务ID>"
	case len(by["ready"]) > 0:
		next = "有可领取任务：在对应工程 maw register && maw next（Worker 名：{应用}-{工程}-{序号}）"
	case len(by["conflict"]) > 0:
		next = "有冲突任务：在对应 worktree 修复后 maw submit；或 maw merge --abort --task <id>"
	case total == 0:
		next = "尚无任务：进入工程编辑蓝图后执行 maw plan apply --yes"
	case done == total:
		next = "当前筛选范围内全部完成"
	}
	if agents == nil {
		agents = []*db.Agent{}
	}
	if tasks == nil {
		tasks = []*db.Task{}
	}
	snap := Snapshot{
		// 数据水位来自数据本身；生成时刻单独记，避免轮询把 updated_at 一直往前推。
		UpdatedAt:   dataWatermark(tasks, agents),
		GeneratedAt: time.Now().Local().Format(SnapshotTimeLayout),
		MaxParallel: maxParallel,
		Total:       total,
		Done:        done,
		Active:      active,
		Blocked:     blocked,
		Percent:     pct,
		NextHint:    next,
		Risks:       risks,
		Problems:    BuildProblems(tasks, agents),
		Counts:      counts,
		Tasks:       tasks,
		Agents:      agents,
		// 与 agents 同一批事实推导，避免快照里两个字段对不上。
		AgentActivity: BuildAgentActivity(agents, time.Now().UTC()),
	}
	snap.Normalize()
	return snap
}

// TaskView 是看板任务列表的显示范围。零值＝默认活动视图：
// 不含已完成、不含已归档，避免完成题长期占着日常视图。
type TaskView struct {
	IncludeDone     bool
	IncludeArchived bool
}

// ParseTaskView 解析 /api/board 的 include 参数：逗号分隔的
// done / archived / all。未知值忽略，不报错、不猜。
func ParseTaskView(include string) TaskView {
	v := TaskView{}
	for _, part := range strings.Split(include, ",") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "done":
			v.IncludeDone = true
		case "archived":
			v.IncludeArchived = true
		case "all":
			v.IncludeDone, v.IncludeArchived = true, true
		}
	}
	return v
}

// WithTaskView 返回按视图过滤任务列表的快照副本，原快照不变。
//
// 只改 Tasks 列表：Total/Done/Percent/Counts 仍描述全量数据，
// 「隐藏已完成」不会让进度条失真。叠加迭代筛选时应先 WithIteration
// 再 WithTaskView，否则迭代重算会基于已过滤的列表。
//
// 过滤时仍保留活动题直接依赖的上游（含 done），避免 chips／拓扑把上游标成未知。
func (s Snapshot) WithTaskView(v TaskView) Snapshot {
	out := s
	out.Tasks = db.FilterActivityKeepingUpstream(s.Tasks, v.IncludeDone, v.IncludeArchived)
	out.IncludeDone = v.IncludeDone
	out.IncludeArchived = v.IncludeArchived
	out.Normalize()
	return out
}

// WithIteration 返回只含指定迭代任务的快照副本，并按这批任务重算聚合。
//
// 迭代是任务的容器：筛到某个迭代后 total/done/percent/risks/next_hint 必须跟着
// 这批任务走，否则卡片上的「3/10」会与列表里那 3 行对不上。
// iterationID 为空串表示「未归属任何迭代」，这是一个合法筛选值，不是「不筛选」；
// 不筛选时调用方根本不该调用本方法（FilterIteration 因此保持 nil）。
//
// 装配层的水位、工程列表、基础筛选与 EventID 来自同一份装配结果，重算不得覆盖它们。
func (s Snapshot) WithIteration(iterationID string) Snapshot {
	kept := make([]*db.Task, 0, len(s.Tasks))
	for _, t := range s.Tasks {
		if t != nil && t.IterationID == iterationID {
			kept = append(kept, t)
		}
	}
	out := aggregate(kept, s.Agents, s.MaxParallel)
	out.EventID = s.EventID
	out.UpdatedAt = s.UpdatedAt
	out.GeneratedAt = s.GeneratedAt
	out.Projects = s.Projects
	// 迭代清单是下拉候选全集，与当前任务筛选无关，必须原样保留。
	out.Iterations = s.Iterations
	out.FilterProject = s.FilterProject
	out.FilterAgent = s.FilterAgent
	out.FilterStatus = s.FilterStatus
	out.Global = s.Global
	id := iterationID
	out.FilterIteration = &id
	out.Normalize()
	return out
}

// IterationFilter 回显当前的迭代筛选：ok=false 表示没按迭代筛（含「筛选空迭代」的区别）。
func (s Snapshot) IterationFilter() (string, bool) {
	if s.FilterIteration == nil {
		return "", false
	}
	return *s.FilterIteration, true
}

// Normalize 把集合字段归一为非 nil 空切片，保证 JSON 恒为 [] 而不是 null。
// 装配层（空库、未知工程、无 risks）可以覆盖字段，因此导出快照前必须再归一一次。
func (s *Snapshot) Normalize() {
	if s == nil {
		return
	}
	if s.Tasks == nil {
		s.Tasks = []*db.Task{}
	}
	if s.Agents == nil {
		s.Agents = []*db.Agent{}
	}
	if s.AgentActivity == nil {
		s.AgentActivity = map[string]string{}
	}
	if s.Projects == nil {
		s.Projects = []*db.Project{}
	}
	if s.Risks == nil {
		s.Risks = []string{}
	}
	if s.Problems == nil {
		s.Problems = ProblemList{}
	}
	if s.Counts == nil {
		s.Counts = map[string]int{}
	}
}

// MarkDataWatermark 用装配层的真实事件水位（事件表最近 ts）覆盖推算值。
// 传空或不可解析时间表示「没有任何数据水位」，updated_at 归零而不是伪造当前时间。
func (s *Snapshot) MarkDataWatermark(ts string) {
	if s == nil {
		return
	}
	if t, ok := parseSnapshotTime(ts); ok {
		s.UpdatedAt = t.Local().Format(SnapshotTimeLayout)
		return
	}
	s.UpdatedAt = ""
}

// dataWatermark 从任务心跳与 Agent 活跃时间里取最大值，作为「数据最近变动」的近似水位。
func dataWatermark(tasks []*db.Task, agents []*db.Agent) string {
	latest, ok := time.Time{}, false
	consider := func(raw string) {
		t, parsed := parseSnapshotTime(raw)
		if !parsed {
			return
		}
		if !ok || t.After(latest) {
			latest, ok = t, true
		}
	}
	for _, t := range tasks {
		if t != nil {
			consider(t.Heartbeat)
		}
	}
	for _, a := range agents {
		if a != nil {
			consider(a.LastSeen)
			consider(a.RegisteredAt)
		}
	}
	if !ok {
		return ""
	}
	return latest.Local().Format(SnapshotTimeLayout)
}

// parseSnapshotTime 兼容状态库里的 RFC3339（UTC）与看板既有展示格式。
func parseSnapshotTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, SnapshotTimeLayout, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func RenderTerminal(s Snapshot) string {
	tasks, agents, maxParallel := s.Tasks, s.Agents, s.MaxParallel
	by := byStatus(tasks)
	filled := 12 * s.Percent / 100
	if s.Percent > 0 && filled == 0 {
		filled = 1
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 12-filled)
	var b strings.Builder
	title := i18n.T("term.global_progress")
	if s.FilterProject != "" {
		title = i18n.T("term.project_progress")
	}
	fmt.Fprintf(&b, "%s %s %d/%d (%d%%)  %s %d  %s %d\n",
		title, bar, s.Done, s.Total, s.Percent, i18n.T("term.parallel"), s.Active, i18n.T("term.blocked_n"), s.Blocked)
	if len(s.Projects) > 0 {
		fmt.Fprintf(&b, i18n.T("term.projects_wired"), len(s.Projects))
		if s.FilterProject != "" || s.FilterAgent != "" || s.FilterStatus != "" {
			b.WriteString(i18n.T("term.filter"))
			parts := []string{}
			if s.FilterProject != "" {
				parts = append(parts, fmt.Sprintf(i18n.T("term.filter_project"), s.FilterProject))
			}
			if s.FilterAgent != "" {
				parts = append(parts, fmt.Sprintf(i18n.T("term.filter_agent"), s.FilterAgent))
			}
			if s.FilterStatus != "" {
				parts = append(parts, fmt.Sprintf(i18n.T("term.filter_status"), s.FilterStatus))
			}
			b.WriteString(strings.Join(parts, " "))
		}
		b.WriteString("\n")
	}
	updated := s.UpdatedAt
	if updated == "" {
		updated = i18n.T("term.none")
	}
	fmt.Fprintf(&b, i18n.T("term.updated_at")+"\n\n", updated)
	activeAgents := make([]*db.Agent, 0, len(agents))
	for _, a := range agents {
		if a != nil && a.Status != "idle" {
			activeAgents = append(activeAgents, a)
		}
	}
	if len(activeAgents) > 0 {
		fmt.Fprintf(&b, "【%s】\n", i18n.T("term.active_agents"))
		for _, a := range activeAgents {
			task := a.TaskID
			if task == "" {
				task = i18n.T("term.none")
			}
			proj := a.ProjectName
			if proj == "" {
				proj = a.ProjectID
			}
			st := agentStatusLabel(a.Status)
			fmt.Fprintf(&b, "  [%s] %s  [%s]  %s=%s  %s=%s\n", proj, a.ID, st, i18n.T("term.task"), task, i18n.T("term.last_seen"), a.LastSeen)
		}
		b.WriteString("\n")
	}
	for _, key := range []string{"ready", "in_progress", "in_review", "merging", "done"} {
		label := i18n.StatusLabel(key)
		items := by[key]
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "【%s】%d\n", label, len(items))
		for _, t := range items {
			owner := t.Owner
			if owner == "" {
				owner = i18n.T("term.unassigned")
			}
			proj := t.ProjectName
			if proj == "" {
				proj = t.ProjectID
			}
			fmt.Fprintf(&b, "  [%s] %s  %s  · %s  %s\n", proj, t.ID, truncate(t.Title, 28), owner, t.Heartbeat)
		}
	}
	if len(by["blocked"]) > 0 {
		fmt.Fprintf(&b, "【%s】\n", i18n.T("term.blocked"))
		for _, t := range by["blocked"] {
			fmt.Fprintf(&b, "  %s（%s）\n", t.ID, t.BlockedReason)
		}
	}
	if len(by["conflict"]) > 0 {
		fmt.Fprintf(&b, "【%s】\n", i18n.T("term.conflict"))
		for _, t := range by["conflict"] {
			fmt.Fprintf(&b, i18n.T("term.conflict_fix")+"\n", t.ID)
		}
	}
	_ = maxParallel
	fmt.Fprintf(&b, "\n"+i18n.T("term.next")+"\n", s.NextHint)
	return b.String()
}

func agentStatusLabel(s string) string {
	switch s {
	case "working", "idle", "offline":
		return i18n.T("agent." + s)
	default:
		return s
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// PageHTML 返回装配后的单文件页面：主题与样式由嵌入资源注入，
// 应用逻辑由 /assets/board-web.js（Vue 3 包）提供。
func PageHTML() string {
	return strings.NewReplacer(
		"{{MAW_THEME_JS}}", strings.TrimSpace(themeJS),
		"{{MAW_STYLES}}", strings.TrimSpace(boardCSS),
	).Replace(pageHTML)
}

//go:embed page.html
var pageHTML string

//go:embed assets/bootstrap/theme.js
var themeJS string

//go:embed assets/legacy/board.css
var boardCSS string

// 前端构建产物（internal/board/web 打包，见 web/README.md）随二进制发布。
//
//go:embed assets/dist
var boardDistFS embed.FS

// BoardAssets 返回嵌入的 assets/dist 只读文件系统，路径相对产物根。
func BoardAssets() (fs.FS, error) {
	return fs.Sub(boardDistFS, "assets/dist")
}

// writeBoardAssets 把构建产物复制到静态导出目录，保持与 PageHTML 中
// `assets/board-web.js` 的相对引用一致，使 file:// 打开也能加载。
func writeBoardAssets(dst string) error {
	assets, err := BoardAssets()
	if err != nil {
		return err
	}
	return fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fs.ReadFile(assets, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func Write(home string, snap Snapshot) (string, error) {
	dir := filepath.Join(home, "board")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	snap.Normalize()
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	htmlPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(htmlPath, []byte(PageHTML()), 0o644); err != nil {
		return "", err
	}
	if err := writeBoardAssets(filepath.Join(dir, "assets")); err != nil {
		return "", err
	}
	md := "```\n" + RenderTerminal(snap) + "```\n"
	_ = os.WriteFile(filepath.Join(dir, "BOARD.md"), []byte(md), 0o644)
	return htmlPath, nil
}

// LoadFunc 从状态库加载最新快照（支持筛选）
type LoadFunc func(projectID, agent, status string) (Snapshot, error)

// TaskDetail 任务详情（含事件日志）
type TaskDetail struct {
	Task    *db.Task    `json:"task"`
	Project *db.Project `json:"project,omitempty"`
	Events  []*db.Event `json:"events"`
	OK      bool        `json:"ok"`
}

// TaskDetailFunc 按工程/任务 ID 加载详情与日志
type TaskDetailFunc func(projectID, taskID string) (TaskDetail, error)

// newHandler serves board, snapshots, details and SSE (compat wrapper).
func newHandler(load LoadFunc, detail TaskDetailFunc) http.Handler {
	return newHandlerOpts(ServeOpts{Load: load, Detail: detail})
}
