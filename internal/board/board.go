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

// Snapshot 看板数据（供 HTML 轮询 / API）
type Snapshot struct {
	// EventID also invalidates open task logs when only an event was appended.
	EventID       int64          `json:"-"`
	UpdatedAt     string         `json:"updated_at"`
	MaxParallel   int            `json:"max_parallel"`
	Total         int            `json:"total"`
	Done          int            `json:"done"`
	Active        int            `json:"active"`
	Blocked       int            `json:"blocked"`
	Percent       int            `json:"percent"`
	NextHint      string         `json:"next_hint"`
	Risks         []string       `json:"risks"`
	Counts        map[string]int `json:"counts"`
	Tasks         []*db.Task     `json:"tasks"`
	Agents        []*db.Agent    `json:"agents"`
	Projects      []*db.Project  `json:"projects"`
	FilterProject string         `json:"filter_project"`
	FilterAgent   string         `json:"filter_agent"`
	FilterStatus  string         `json:"filter_status"`
	Global        bool           `json:"global"`
}

func byStatus(tasks []*db.Task) map[string][]*db.Task {
	m := map[string][]*db.Task{}
	for _, t := range tasks {
		m[t.Status] = append(m[t.Status], t)
	}
	return m
}

func BuildSnapshot(tasks []*db.Task, agents []*db.Agent, maxParallel int) Snapshot {
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
	var risks []string
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
		next = "有冲突任务：在对应 worktree 修复后 maw submit"
	case total == 0:
		next = "尚无任务：进入工程编辑蓝图后执行 maw plan apply --yes"
	case done == total:
		next = "当前筛选范围内全部完成"
	}
	if agents == nil {
		agents = []*db.Agent{}
	}
	return Snapshot{
		UpdatedAt:   time.Now().Local().Format("2006-01-02 15:04:05"),
		MaxParallel: maxParallel,
		Total:       total,
		Done:        done,
		Active:      active,
		Blocked:     blocked,
		Percent:     pct,
		NextHint:    next,
		Risks:       risks,
		Counts:      counts,
		Tasks:       tasks,
		Agents:      agents,
		Projects:    []*db.Project{},
		Global:      true,
	}
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
	fmt.Fprintf(&b, i18n.T("term.updated_at")+"\n\n", s.UpdatedAt)
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
