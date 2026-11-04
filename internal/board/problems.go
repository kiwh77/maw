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
			problems = append(problems, Problem{
				ProjectID: t.ProjectID, ProjectName: t.ProjectName, TaskID: t.ID,
				Kind: ProblemKindBlocked, Severity: ProblemSeverityHigh,
				Message: reason, TS: t.Heartbeat,
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
