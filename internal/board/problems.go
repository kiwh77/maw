package board

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

// 问题 kind 常量。事件类问题（question/blocker 事件）本轮不聚合：
// COL-MSG-001 落地后由装配层在此补充对应分支。
const (
	ProblemKindBlocked  = "blocked"
	ProblemKindConflict = "conflict"
	ProblemKindRetry    = "retry"
	ProblemKindStalled  = "stalled"
	ProblemKindDepWait  = "dep-wait"
	ProblemKindQuestion = "question"
	ProblemKindBlocker  = "blocker"
)

// severity 三档：blocked/conflict > stalled/retry > dep-wait（question/blocker 落地后取中低档）。
const (
	ProblemSeverityHigh   = "high"
	ProblemSeverityMedium = "medium"
	ProblemSeverityLow    = "low"
)

// stalledThreshold 是 Agent「停滞」的判定窗口：working 状态但 last_seen 超过该时长。
const stalledThreshold = 30 * time.Minute

// Problem 看板一屏审查的单条运行问题（只读，不含处理动作）。
type Problem struct {
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name,omitempty"`
	TaskID      string `json:"task_id"`
	Kind        string `json:"kind"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Category    string `json:"category,omitempty"` // block 类别：scope/env/gate/…
	TS          string `json:"ts"`
}

// ProblemList 把 nil problems 固定映射成 JSON []，语义等同「这批数据没有问题」
// 而不是「problems 缺失」；装配层即便漏掉 Normalize 也不会让前端拿到 null。
type ProblemList []Problem

// MarshalJSON 见 Snapshot 里的 TaskList 等命名列表类型。
func (l ProblemList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]Problem(l))
}

// BuildProblems 聚合这批任务 + Agent 的运行问题：
// ① blocked（最高档，message=blocked_reason）② conflict（最高档）
// ③ retry：attempts>=2 且未完成（中档）⑤ stalled：working 但 last_seen 超 30 分钟（中档）
// ⑥ dep-wait：backlog/ready 引用未完成 deps，引用查不到视为未完成（低档）。
// ④ question/blocker 事件类本轮不做（COL-MSG-001 落地后补充）。
// 同档按 (project_id, task_id) 稳定排序；空输入返回空切片而非 nil。
func BuildProblems(tasks []*db.Task, agents []*db.Agent) []Problem {
	problems := []Problem{}
	byID := map[string]*db.Task{}
	for _, t := range tasks {
		if t != nil {
			byID[t.ID] = t
		}
	}
	for _, t := range tasks {
		if t == nil {
			continue
		}
		switch t.Status {
		case "blocked":
			reason := t.BlockedReason
			if reason == "" {
				reason = "未填写原因"
			}
			cat := ""
			if t.Extra != nil {
				if v, ok := t.Extra["block_category"].(string); ok {
					cat = strings.TrimSpace(v)
				}
			}
			if cat != "" {
				reason = "[" + cat + "] " + reason
			}
			problems = append(problems, Problem{
				ProjectID: t.ProjectID, ProjectName: t.ProjectName, TaskID: t.ID,
				Kind: ProblemKindBlocked, Severity: ProblemSeverityHigh,
				Message: reason, Category: cat, TS: t.Heartbeat,
			})
		case "conflict":
			problems = append(problems, Problem{
				ProjectID: t.ProjectID, ProjectName: t.ProjectName, TaskID: t.ID,
				Kind: ProblemKindConflict, Severity: ProblemSeverityHigh,
				Message: "合并冲突，需作者修复后重新提交", TS: t.Heartbeat,
			})
		}
		if t.Attempts >= 2 && t.Status != "done" {
			problems = append(problems, Problem{
				ProjectID: t.ProjectID, ProjectName: t.ProjectName, TaskID: t.ID,
				Kind: ProblemKindRetry, Severity: ProblemSeverityMedium,
				Message: fmt.Sprintf("已尝试 %d 次仍未完成", t.Attempts), TS: t.Heartbeat,
			})
		}
		if t.Status == "backlog" || t.Status == "ready" {
			waiting := []string{}
			for _, d := range t.Deps {
				if up := byID[d]; up == nil || up.Status != "done" {
					waiting = append(waiting, d)
				}
			}
			if len(waiting) > 0 {
				problems = append(problems, Problem{
					ProjectID: t.ProjectID, ProjectName: t.ProjectName, TaskID: t.ID,
					Kind: ProblemKindDepWait, Severity: ProblemSeverityLow,
					Message: "等待依赖：" + strings.Join(waiting, "、"), TS: t.Heartbeat,
				})
			}
		}
	}
	stallDeadline := time.Now().Add(-stalledThreshold)
	for _, a := range agents {
		if a == nil || a.Status != "working" {
			continue
		}
		lastSeen, ok := parseSnapshotTime(a.LastSeen)
		if !ok || !lastSeen.Before(stallDeadline) {
			continue
		}
		projID, projName := a.ProjectID, a.ProjectName
		if t := byID[a.TaskID]; t != nil {
			if projID == "" {
				projID = t.ProjectID
			}
			if projName == "" {
				projName = t.ProjectName
			}
		}
		problems = append(problems, Problem{
			ProjectID: projID, ProjectName: projName, TaskID: a.TaskID,
			Kind: ProblemKindStalled, Severity: ProblemSeverityMedium,
			Message: fmt.Sprintf("Agent %s 已停滞超过 30 分钟（最近活跃 %s）", a.ID, a.LastSeen),
			TS:      a.LastSeen,
		})
	}
	sort.SliceStable(problems, func(i, j int) bool {
		pi, pj := problems[i], problems[j]
		if ri, rj := severityRank(pi.Severity), severityRank(pj.Severity); ri != rj {
			return ri > rj
		}
		if pi.ProjectID != pj.ProjectID {
			return pi.ProjectID < pj.ProjectID
		}
		return pi.TaskID < pj.TaskID
	})
	return problems
}

func severityRank(s string) int {
	switch s {
	case ProblemSeverityHigh:
		return 3
	case ProblemSeverityMedium:
		return 2
	case ProblemSeverityLow:
		return 1
	}
	return 0
}

// ProblemKindRouter 是「Router 已经介入」的条目：有人（系统 Agent）在处理这条阻塞。
const ProblemKindRouter = "router"

// ProblemKindRouterIdle 是「阻塞还没有 Router 接管」的条目。
//
// 单独一个 kind 而不是复用 blocked：blocked 回答「这件事卡住了」，
// router-idle 回答「没人会来救它」——后者才是需要人动手的那一条。
const ProblemKindRouterIdle = "router-idle"

// routerJobWindow 是「算一次介入」的时间窗：超过它的旧作业不再当作仍在处理。
const routerJobWindow = 24 * time.Hour

// BuildRouterProblems 把阻塞任务与系统作业对账，回答「Router 有没有介入」。
//
// 只看 blocked 任务：其它状态没有「需要被救」的语义。判定按最近一条相关
// 作业（unblock_assist / task_split / run_reconcile）的状态分档：
//   - queued/running → medium，「处理中」；
//   - done          → low，「已介入」，带决策动作与结果摘要；
//   - escalated     → medium，「已升级人工」，带原因；
//   - 没有作业      → medium，router-idle，「无人接管」。
//
// 同档按 (project_id, task_id) 稳定排序，输出与输入顺序无关。
func BuildRouterProblems(tasks []*db.Task, jobs []*db.SystemJob, now time.Time) []Problem {
	now = now.UTC()
	// 每个任务只保留最近一条作业：更早的尝试已经被后来的覆盖。
	latest := map[string]*db.SystemJob{}
	for _, j := range jobs {
		if j == nil || j.TaskID == "" {
			continue
		}
		switch j.Kind {
		case "unblock_assist", "task_split", "run_reconcile":
		default:
			continue
		}
		if ts, err := time.Parse(time.RFC3339, j.CreatedAt); err == nil && now.Sub(ts) > routerJobWindow {
			continue
		}
		key := j.ProjectID + "\x00" + j.TaskID
		if prev, ok := latest[key]; !ok || j.CreatedAt > prev.CreatedAt {
			latest[key] = j
		}
	}
	out := []Problem{}
	for _, t := range tasks {
		if t == nil || t.Status != "blocked" {
			continue
		}
		p := Problem{
			ProjectID: t.ProjectID, ProjectName: t.ProjectName,
			TaskID: t.ID, Severity: ProblemSeverityMedium,
		}
		job := latest[t.ProjectID+"\x00"+t.ID]
		if job == nil {
			p.Kind = ProblemKindRouterIdle
			p.Message = "阻塞未被 Router 接管：system_agent 未启用，或该处置类型未在 auto_kinds 放行"
			p.TS = t.Heartbeat
			out = append(out, p)
			continue
		}
		p.Kind = ProblemKindRouter
		p.TS = job.CreatedAt
		action := stringOf(job.Decision["action"])
		switch job.Status {
		case "done":
			p.Severity = ProblemSeverityLow
			p.Message = "Router 已介入：" + job.Kind
			if action != "" {
				p.Message += " → " + action
			}
			if note := stringOf(job.Result["note"]); note != "" {
				p.Message += "（" + note + "）"
			}
		case "escalated":
			p.Message = "Router 已升级人工：" + firstNonEmptyText(stringOf(job.Decision["reason"]), job.Reason)
		case "queued", "running":
			p.Message = fmt.Sprintf("Router 处理中：%s（第 %d/%d 次）", job.Kind, job.Attempts, job.MaxAttempts)
		case "failed":
			p.Message = "Router 处理失败：" + firstNonEmptyText(stringOf(job.Result["error"]), job.Reason)
		default:
			p.Message = "Router 状态：" + job.Status + "（" + job.Kind + "）"
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		if out[i].TaskID != out[j].TaskID {
			return out[i].TaskID < out[j].TaskID
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmptyText(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return "未提供原因"
}
