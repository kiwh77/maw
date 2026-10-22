package board

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wuhao/maw/internal/db"
)

// StatusLabel 状态码 → 中文
var StatusLabel = map[string]string{
	"backlog":     "待排期",
	"ready":       "可领取",
	"claimed":     "已领取",
	"in_progress": "进行中",
	"self_check":  "自检中",
	"in_review":   "评审中",
	"merging":     "合并中",
	"conflict":    "冲突",
	"blocked":     "阻塞",
	"done":        "已完成",
	"cancelled":   "已取消",
}

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
	title := "全局进度"
	if s.FilterProject != "" {
		title = "工程进度"
	}
	fmt.Fprintf(&b, "%s %s %d/%d（%d%%）  并行 %d  阻塞 %d\n",
		title, bar, s.Done, s.Total, s.Percent, s.Active, s.Blocked)
	if len(s.Projects) > 0 {
		fmt.Fprintf(&b, "已接入工程 %d", len(s.Projects))
		if s.FilterProject != "" || s.FilterAgent != "" || s.FilterStatus != "" {
			fmt.Fprintf(&b, "  筛选：")
			parts := []string{}
			if s.FilterProject != "" {
				parts = append(parts, "工程="+s.FilterProject)
			}
			if s.FilterAgent != "" {
				parts = append(parts, "Agent="+s.FilterAgent)
			}
			if s.FilterStatus != "" {
				parts = append(parts, "状态="+s.FilterStatus)
			}
			b.WriteString(strings.Join(parts, " "))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "更新于 %s\n\n", s.UpdatedAt)
	activeAgents := make([]*db.Agent, 0, len(agents))
	for _, a := range agents {
		if a != nil && a.Status != "idle" {
			activeAgents = append(activeAgents, a)
		}
	}
	if len(activeAgents) > 0 {
		b.WriteString("【执行中 Agent】\n")
		for _, a := range activeAgents {
			task := a.TaskID
			if task == "" {
				task = "无"
			}
			proj := a.ProjectName
			if proj == "" {
				proj = a.ProjectID
			}
			st := agentStatusZH(a.Status)
			fmt.Fprintf(&b, "  [%s] %s  [%s]  任务=%s  最近活动=%s\n", proj, a.ID, st, task, a.LastSeen)
		}
		b.WriteString("\n")
	}
	for _, key := range []string{"ready", "in_progress", "in_review", "merging", "done"} {
		label := StatusLabel[key]
		items := by[key]
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "【%s】%d\n", label, len(items))
		for _, t := range items {
			owner := t.Owner
			if owner == "" {
				owner = "未分配"
			}
			proj := t.ProjectName
			if proj == "" {
				proj = t.ProjectID
			}
			fmt.Fprintf(&b, "  [%s] %s  %s  · %s  %s\n", proj, t.ID, truncate(t.Title, 28), owner, t.Heartbeat)
		}
	}
	if len(by["blocked"]) > 0 {
		b.WriteString("【阻塞】\n")
		for _, t := range by["blocked"] {
			fmt.Fprintf(&b, "  %s（%s）\n", t.ID, t.BlockedReason)
		}
	}
	if len(by["conflict"]) > 0 {
		b.WriteString("【冲突】\n")
		for _, t := range by["conflict"] {
			fmt.Fprintf(&b, "  %s — 修复后重新提交\n", t.ID)
		}
	}
	_ = maxParallel
	fmt.Fprintf(&b, "\n下一步：%s\n", s.NextHint)
	return b.String()
}

func agentStatusZH(s string) string {
	switch s {
	case "working":
		return "执行中"
	case "idle":
		return "空闲"
	case "offline":
		return "离线"
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

func PageHTML() string {
	return pageHTML
}

//go:embed page.html
var pageHTML string

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

// newHandler serves the read-only board, snapshots, details and SSE invalidations.
func newHandler(load LoadFunc, detail TaskDetailFunc) http.Handler {
	mux := http.NewServeMux()
	events := newEventHub(load, 500*time.Millisecond)
	mux.Handle("/api/events", events.handler(15*time.Second))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(PageHTML()))
	})
	mux.HandleFunc("/api/board", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		snap, err := load(q.Get("project"), q.Get("agent"), q.Get("status"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(snap)
	})
	mux.HandleFunc("/api/task", func(w http.ResponseWriter, r *http.Request) {
		if detail == nil {
			http.Error(w, "task detail unavailable", http.StatusNotImplemented)
			return
		}
		q := r.URL.Query()
		taskID := q.Get("id")
		if taskID == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		d, err := detail(q.Get("project"), taskID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if d.Task == nil {
			http.Error(w, "task not found", http.StatusNotFound)
			return
		}
		d.OK = true
		if d.Events == nil {
			d.Events = []*db.Event{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(d)
	})
	mux.HandleFunc("/snapshot.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/board?"+r.URL.RawQuery, http.StatusTemporaryRedirect)
	})

	return mux
}

// Serve 启动只读看板 HTTP 服务（动态读全局库）。
func Serve(addr string, load LoadFunc, detail TaskDetailFunc) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	url := "http://" + ln.Addr().String() + "/"
	go func() {
		_ = http.Serve(ln, newHandler(load, detail))
	}()
	return url, nil
}
