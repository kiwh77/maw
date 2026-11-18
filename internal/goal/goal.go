// Package goal 是目标级 Lead 的领域模型：目标身份、目标版本、验收条件、
// 预算与目标状态机（契约 maw.lead.goal/v1 §2–§4）。
//
// 本包只做纯计算与判定，不碰数据库、不起进程、不调模型；持久化在
// internal/db/goal.go，规划与入队在 PI-302／PI-303 落地。
package goal

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// 目标状态（契约 §3，封闭集合）。
const (
	StatusDraft     = "draft"
	StatusActive    = "active"
	StatusVerifying = "verifying"
	StatusPaused    = "paused"
	StatusDone      = "done"
	StatusEscalated = "escalated"
	StatusAbandoned = "abandoned"
)

// 暂停与升级原因（契约 §3、§11）。
const (
	ReasonHumanPause     = "human_pause"
	ReasonPermissionDeny = "permission_denied"
	ReasonBudgetExhaust  = "budget_exhausted"
	ReasonInvariant      = "invariant_violation"
)

// 验收条件形态（契约 §4）。
const (
	AcceptanceCommand  = "command"
	AcceptanceManual   = "manual"
	AcceptanceEvidence = "evidence"
)

// 目标判别码：与契约 §14 的 G1xx 对齐，新增码必须先改契约。
const (
	CodeGoalIDInvalid   = "G110"
	CodeGoalVersion     = "G120"
	CodeAcceptanceField = "G121"
	CodeGoalTransition  = "G123"
)

// ErrGoalNotFound 表示工程里没有这个目标。
var ErrGoalNotFound = errors.New("goal: 目标不存在")

// ErrGoalTransition 表示请求的目标状态迁移不在允许集合内。
var ErrGoalTransition = errors.New("goal: 非法目标状态迁移")

// ErrGoalVersionConflict 表示版本号不是「当前最大版本 + 1」。
var ErrGoalVersionConflict = errors.New("goal: 目标版本冲突")

// goalIDRE 是目标身份形态：gl_ + 至少 8 位小写字母数字（G110）。
var goalIDRE = regexp.MustCompile(`^gl_[a-z0-9]{8,}$`)

// goalTransitions 是契约 §3 的状态机：只允许沿声明边推进，终态不再出发。
var goalTransitions = map[string][]string{
	StatusDraft:     {StatusActive},
	StatusActive:    {StatusVerifying, StatusPaused, StatusEscalated, StatusAbandoned},
	StatusVerifying: {StatusActive, StatusDone, StatusPaused, StatusEscalated, StatusAbandoned},
	StatusPaused:    {StatusActive, StatusAbandoned},
	StatusEscalated: {StatusPaused, StatusAbandoned},
	StatusDone:      {},
	StatusAbandoned: {},
}

// AllStatuses 返回全部目标状态，顺序与契约 §3 一致。
func AllStatuses() []string {
	return []string{StatusDraft, StatusActive, StatusVerifying, StatusPaused, StatusDone, StatusEscalated, StatusAbandoned}
}

// ValidStatus 报告状态是否属于封闭集合。
func ValidStatus(status string) bool {
	for _, s := range AllStatuses() {
		if s == status {
			return true
		}
	}
	return false
}

// IsTerminal 报告状态是否终态（不再出发任何迁移）。
func IsTerminal(status string) bool {
	return status == StatusDone || status == StatusAbandoned
}

// CanTransition 报告 from → to 是否合法；同状态不算迁移（幂等），一律返回 false。
func CanTransition(from, to string) bool {
	for _, next := range goalTransitions[strings.TrimSpace(from)] {
		if next == strings.TrimSpace(to) {
			return true
		}
	}
	return false
}

// ValidReason 报告暂停／升级原因是否属于契约 §3 的集合。
func ValidReason(reason string) bool {
	switch reason {
	case ReasonHumanPause, ReasonPermissionDeny, ReasonBudgetExhaust, ReasonInvariant:
		return true
	}
	return false
}

// ValidateGoalID 校验目标身份形态（G110）。
func ValidateGoalID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%s: goal_id 为空", CodeGoalIDInvalid)
	}
	if !goalIDRE.MatchString(id) {
		return fmt.Errorf("%s: goal_id %q 必须形如 gl_<至少 8 位小写字母数字>", CodeGoalIDInvalid, id)
	}
	return nil
}

// Goal 是目标的身份与生命周期，不含定义内容。
//
// 定义（标题／陈述／验收／预算）按目标版本快照存放（Version），
// 避免「改一次验收条件就把历史修订的判据一起改掉」。
type Goal struct {
	ProjectID     string `json:"project_id"`
	GoalID        string `json:"goal_id"`
	IterationID   string `json:"iteration_id,omitempty"`
	Status        string `json:"status"`
	ActiveVersion int    `json:"active_version"`
	// Revision 是乐观锁版本：每次状态或字段更新 +1。
	Revision      int    `json:"revision"`
	AbandonReason string `json:"abandon_reason,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// Validate 校验目标的身份字段与状态。
func (g *Goal) Validate() error {
	if g == nil {
		return errors.New("goal: 目标为空")
	}
	if strings.TrimSpace(g.ProjectID) == "" {
		return errors.New("goal: project_id 为空")
	}
	if err := ValidateGoalID(g.GoalID); err != nil {
		return err
	}
	if g.Status == "" {
		g.Status = StatusDraft
	}
	if !ValidStatus(g.Status) {
		return fmt.Errorf("%s: 非法目标状态 %q", CodeGoalTransition, g.Status)
	}
	if g.ActiveVersion < 1 {
		return fmt.Errorf("%s: active_version 必须 >= 1", CodeGoalVersion)
	}
	if g.Revision < 0 {
		return fmt.Errorf("goal: revision 不能为负")
	}
	return nil
}

// Acceptance 是一条目标级验收条件（契约 §4）。
type Acceptance struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Check            string `json:"check"`
	Statement        string `json:"statement"`
	EvidenceRequired bool   `json:"evidence_required"`
}

// Validate 校验单条验收条件：四列齐全、形态封闭、id 非空。
func (a Acceptance) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("%s: 验收条件缺 id", CodeAcceptanceField)
	}
	switch strings.TrimSpace(a.Kind) {
	case AcceptanceCommand, AcceptanceManual, AcceptanceEvidence:
	default:
		return fmt.Errorf("%s: %s 的 kind %q 不在 command/manual/evidence 内", CodeAcceptanceField, a.ID, a.Kind)
	}
	if strings.TrimSpace(a.Check) == "" {
		return fmt.Errorf("%s: %s 缺 check", CodeAcceptanceField, a.ID)
	}
	if strings.TrimSpace(a.Statement) == "" {
		return fmt.Errorf("%s: %s 缺 statement", CodeAcceptanceField, a.ID)
	}
	return nil
}

// Budget 是目标预算（契约 §10）。计数单位固定，缺省值由 WithDefaults 补齐。
type Budget struct {
	ModelCalls          int   `json:"model_calls"`
	TokensIn            int64 `json:"tokens_in"`
	TokensOut           int64 `json:"tokens_out"`
	WallClockSeconds    int64 `json:"wall_clock_seconds"`
	MaxPlanRevisions    int   `json:"max_plan_revisions"`
	MaxReworkRounds     int   `json:"max_rework_rounds"`
	MaxCardsPerRevision int   `json:"max_cards_per_revision"`
}

// DefaultBudget 是契约 §10／§16 的缺省预算。
func DefaultBudget() Budget {
	return Budget{
		ModelCalls:          200,
		TokensIn:            4_000_000,
		TokensOut:           800_000,
		WallClockSeconds:    28_800,
		MaxPlanRevisions:    5,
		MaxReworkRounds:     2,
		MaxCardsPerRevision: 30,
	}
}

// WithDefaults 把非正值替换成缺省值；显式给的正值一律保留。
func (b Budget) WithDefaults() Budget {
	d := DefaultBudget()
	if b.ModelCalls <= 0 {
		b.ModelCalls = d.ModelCalls
	}
	if b.TokensIn <= 0 {
		b.TokensIn = d.TokensIn
	}
	if b.TokensOut <= 0 {
		b.TokensOut = d.TokensOut
	}
	if b.WallClockSeconds <= 0 {
		b.WallClockSeconds = d.WallClockSeconds
	}
	if b.MaxPlanRevisions <= 0 {
		b.MaxPlanRevisions = d.MaxPlanRevisions
	}
	if b.MaxReworkRounds <= 0 {
		b.MaxReworkRounds = d.MaxReworkRounds
	}
	if b.MaxCardsPerRevision <= 0 {
		b.MaxCardsPerRevision = d.MaxCardsPerRevision
	}
	return b
}

// Exhausted 报告任一已观测维度是否达到上限。
//
// unknown 维度（如拿不到用量）不在这里判：契约 G401 要求 unknown 时由调用方
// 进 escalated，不得当成「未超」。
func (b Budget) Exhausted(used Budget) bool {
	if b.ModelCalls > 0 && used.ModelCalls >= b.ModelCalls {
		return true
	}
	if b.TokensIn > 0 && used.TokensIn >= b.TokensIn {
		return true
	}
	if b.TokensOut > 0 && used.TokensOut >= b.TokensOut {
		return true
	}
	if b.WallClockSeconds > 0 && used.WallClockSeconds >= b.WallClockSeconds {
		return true
	}
	return false
}

// Version 是一次冻结的目标定义（契约 §2）。
type Version struct {
	ProjectID   string       `json:"project_id"`
	GoalID      string       `json:"goal_id"`
	GoalVersion int          `json:"goal_version"`
	Title       string       `json:"title"`
	Statement   string       `json:"statement"`
	Acceptance  []Acceptance `json:"acceptance"`
	Budget      Budget       `json:"budget"`
	FrozenAt    string       `json:"frozen_at"`
}

// Validate 校验目标版本：标题／陈述非空、验收条件非空且逐条合法、id 不重复。
func (v *Version) Validate() error {
	if v == nil {
		return errors.New("goal: 目标版本为空")
	}
	if err := ValidateGoalID(v.GoalID); err != nil {
		return err
	}
	if v.GoalVersion < 1 {
		return fmt.Errorf("%s: goal_version 必须 >= 1", CodeGoalVersion)
	}
	if strings.TrimSpace(v.Title) == "" {
		return fmt.Errorf("%s: 目标版本缺 title", CodeGoalVersion)
	}
	if strings.TrimSpace(v.Statement) == "" {
		return fmt.Errorf("%s: 目标版本缺 statement", CodeGoalVersion)
	}
	if len(v.Acceptance) == 0 {
		return fmt.Errorf("%s: 目标版本至少需要 1 条验收条件", CodeAcceptanceField)
	}
	seen := map[string]bool{}
	for _, a := range v.Acceptance {
		if err := a.Validate(); err != nil {
			return err
		}
		id := strings.TrimSpace(a.ID)
		if seen[id] {
			return fmt.Errorf("%s: 验收条件 id %q 重复", CodeAcceptanceField, id)
		}
		seen[id] = true
	}
	v.Budget = v.Budget.WithDefaults()
	return nil
}
