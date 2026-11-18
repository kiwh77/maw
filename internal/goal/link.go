package goal

import (
	"errors"
	"fmt"
	"strings"
)

// 任务映射状态（契约 §7）。
const (
	LinkApplied = "applied"
	LinkDrifted = "drifted"
)

// 入队台账状态（契约 §8.1）。
const (
	EnqueueAppended = "appended"
	EnqueueApplied  = "applied"
	EnqueueFailed   = "failed"
)

// 入队与映射的稳定错误形状：调用方按 errors.Is 判定，不用比字符串。
var (
	// ErrTaskLinkConflict 表示同一任务实例已被另一条映射占用，或同键换了内容。
	ErrTaskLinkConflict = errors.New("goal: 任务映射冲突")
	// ErrEnqueueKeyReuse 表示同一幂等键配了不同摘要（契约 G301：不得以最新为准）。
	ErrEnqueueKeyReuse = errors.New("goal: 幂等键被复用于不同请求")
	// ErrPlanNotFound 表示目标下没有这个修订。
	ErrPlanNotFound = errors.New("goal: 计划修订不存在")
	// ErrPlanTransition 表示计划修订的状态迁移不合法。
	ErrPlanTransition = errors.New("goal: 非法计划修订状态迁移")
	// ErrApprovalDigest 表示批准时给出的摘要与修订内容不一致。
	ErrApprovalDigest = errors.New("goal: 批准摘要与计划修订不一致")
)

// ValidLinkState 报告映射状态是否合法。
func ValidLinkState(state string) bool {
	return state == LinkApplied || state == LinkDrifted
}

// ValidEnqueueState 报告台账状态是否合法。
func ValidEnqueueState(state string) bool {
	return state == EnqueueAppended || state == EnqueueApplied || state == EnqueueFailed
}

// TaskLink 是一条不可变的任务映射（契约 §7）：
// 把「批准的那张卡」钉到具体任务实例与定义版本上，重放与重启都以它为准。
type TaskLink struct {
	ProjectID  string `json:"project_id"`
	GoalID     string `json:"goal_id"`
	PlanRev    int    `json:"plan_rev"`
	CardID     string `json:"card_id"`
	InstanceID string `json:"instance_id"`
	DisplayID  string `json:"display_id"`
	VersionNo  int    `json:"version_no"`
	EnqueueKey string `json:"enqueue_key"`
	State      string `json:"state"`
	AppliedAt  string `json:"applied_at"`
	AppliedBy  string `json:"applied_by,omitempty"`
}

// Validate 校验映射的必填字段与状态。
func (l *TaskLink) Validate() error {
	if l == nil {
		return errors.New("goal: 任务映射为空")
	}
	if strings.TrimSpace(l.ProjectID) == "" || strings.TrimSpace(l.GoalID) == "" {
		return errors.New("goal: 任务映射缺 project_id/goal_id")
	}
	if l.PlanRev < 1 || strings.TrimSpace(l.CardID) == "" {
		return errors.New("goal: 任务映射缺 plan_rev/card_id")
	}
	if strings.TrimSpace(l.InstanceID) == "" || strings.TrimSpace(l.DisplayID) == "" {
		return errors.New("goal: 任务映射缺 instance_id/display_id")
	}
	if l.VersionNo < 1 {
		return fmt.Errorf("goal: 任务映射的 version_no 必须 >= 1")
	}
	if strings.TrimSpace(l.EnqueueKey) == "" {
		return errors.New("goal: 任务映射缺 enqueue_key")
	}
	if l.State == "" {
		l.State = LinkApplied
	}
	if !ValidLinkState(l.State) {
		return fmt.Errorf("goal: 非法映射状态 %q", l.State)
	}
	return nil
}

// SameBinding 报告两条映射是否指向同一目标修订与同一张卡（幂等判据）。
func (l *TaskLink) SameBinding(o *TaskLink) bool {
	if l == nil || o == nil {
		return false
	}
	return l.ProjectID == o.ProjectID &&
		l.GoalID == o.GoalID &&
		l.PlanRev == o.PlanRev &&
		l.CardID == o.CardID &&
		l.InstanceID == o.InstanceID &&
		l.DisplayID == o.DisplayID &&
		l.VersionNo == o.VersionNo &&
		l.EnqueueKey == o.EnqueueKey
}

// EnqueueRecord 是入队台账的一行（契约 §8.1）：
// 先把「这次追加」登记下来，再执行追加与增量 apply，崩溃后据此判断能否重放。
type EnqueueRecord struct {
	ProjectID  string `json:"project_id"`
	EnqueueKey string `json:"enqueue_key"`
	GoalID     string `json:"goal_id"`
	PlanRev    int    `json:"plan_rev"`
	CardID     string `json:"card_id"`
	PlanDigest string `json:"plan_digest"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}

// Validate 校验台账行的必填字段与状态。
func (e *EnqueueRecord) Validate() error {
	if e == nil {
		return errors.New("goal: 入队台账为空")
	}
	if strings.TrimSpace(e.ProjectID) == "" || strings.TrimSpace(e.EnqueueKey) == "" {
		return errors.New("goal: 入队台账缺 project_id/enqueue_key")
	}
	if strings.TrimSpace(e.GoalID) == "" || e.PlanRev < 1 || strings.TrimSpace(e.CardID) == "" {
		return errors.New("goal: 入队台账缺 goal_id/plan_rev/card_id")
	}
	if strings.TrimSpace(e.PlanDigest) == "" {
		return errors.New("goal: 入队台账缺 plan_digest")
	}
	if e.State == "" {
		e.State = EnqueueAppended
	}
	if !ValidEnqueueState(e.State) {
		return fmt.Errorf("goal: 非法台账状态 %q", e.State)
	}
	return nil
}

// RewardProgress 计算目标进度的三列口径（契约 §7）：不合并成百分比。
type Progress struct {
	MappedTotal    int `json:"mapped_total"`
	MappedDone     int `json:"mapped_done"`
	Drifted        int `json:"drifted"`
	PendingEnqueue int `json:"pending_enqueue"`
}

// Progress 按映射与任务状态统计进度；done 由调用方按交付契约给出（这里只收判定结果）。
func ProgressOf(links []*TaskLink, isDone func(*TaskLink) bool, pendingEnqueue int) Progress {
	var p Progress
	for _, l := range links {
		if l == nil {
			continue
		}
		if l.State == LinkDrifted {
			p.Drifted++
			continue
		}
		p.MappedTotal++
		if isDone != nil && isDone(l) {
			p.MappedDone++
		}
	}
	p.PendingEnqueue = pendingEnqueue
	return p
}
