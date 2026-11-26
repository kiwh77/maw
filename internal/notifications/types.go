package notifications

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// Kind 是需要用户介入的提醒类型。取值与 Policy 的可配置项一一对应（F33）。
type Kind string

const (
	// KindWaiting 是「在等人」：运行停在 waiting、任务被 blocked 或被冲突挡住。
	KindWaiting Kind = "waiting"
	// KindFailed 是「已经失败」：运行 failed／timed_out，需要人决定重试还是放弃。
	KindFailed Kind = "failed"
	// KindDelivery 是「交付有变化」：待验收、验收通过、要求修改、任务完成（F28）。
	KindDelivery Kind = "delivery"
)

// Valid 报告类型是否已登记。
func (k Kind) Valid() bool {
	switch k {
	case KindWaiting, KindFailed, KindDelivery:
		return true
	}
	return false
}

// AllKinds 返回全部类型，供配置校验与对齐测试使用。
func AllKinds() []Kind { return []Kind{KindWaiting, KindFailed, KindDelivery} }

// Channel 是投递渠道。站内与桌面托盘共用同一条记录语义，只是投递方式不同。
type Channel string

const (
	// ChannelInApp 是站内列表：记录本身就是提醒，不需要外部投递。
	ChannelInApp Channel = "in_app"
	// ChannelDesktop 是桌面托盘／OS 弹窗：投递不可回滚，也不是原子的。
	ChannelDesktop Channel = "desktop"
)

// Valid 报告渠道是否已登记。
func (c Channel) Valid() bool { return c == ChannelInApp || c == ChannelDesktop }

// AllChannels 返回全部渠道。
func AllChannels() []Channel { return []Channel{ChannelInApp, ChannelDesktop} }

// Status 是记录状态。取值必须落在 notification_deliveries 的 CHECK 集合里，
// 多一个值就是 schema 违约，不是本包能自行扩的枚举。
type Status string

const (
	// StatusPending 已登记、尚未确认投递成功。
	StatusPending Status = "pending"
	// StatusDelivered 投递动作返回成功。不代表用户看见。
	StatusDelivered Status = "delivered"
	// StatusAcked 用户确认。只有它算处理完。
	StatusAcked Status = "acked"
	// StatusFailed 投递失败（可重试，仍是未处理）。
	StatusFailed Status = "failed"
)

// Valid 报告状态是否在 schema 允许的四个值内。
func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusDelivered, StatusAcked, StatusFailed:
		return true
	}
	return false
}

// Handled 报告这条提醒是否已被用户确认。delivered 与 failed 都不算处理完。
func (s Status) Handled() bool { return s == StatusAcked }

// Notification 是一条提醒记录。它是「有身份的提醒」：ID 稳定、可被确认、
// 可被投递多次，且每次尝试都留下痕迹。
type Notification struct {
	ID        string  `json:"notification_id"`
	ProjectID string  `json:"project_id"`
	EventID   string  `json:"event_id"`
	Kind      Kind    `json:"kind"`
	Channel   Channel `json:"channel"`
	// Subject 是用户视角的对象身份（通常是任务实例 ti_xxx），不是事件自身身份：
	// 同一任务的不同 Run 停在 waiting 是同一件事，不该提醒两遍。
	Subject string `json:"subject"`
	// State 是触发提醒时对象所处的状态词（run.phase 或 task.status 的取值）。
	State string `json:"state"`
	// Reason 是 F20 的分类原因（认证失败、限流、断联……），可空。
	Reason string `json:"reason,omitempty"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
	Status Status `json:"status"`
	// CreatedAt／DeliveredAt／AckedAt 是 UTC 定长串（2006-01-02T15:04:05Z），
	// 与 internal/db 的写入口径一致，因此可以直接按字符串比较先后。
	CreatedAt   string `json:"created_at"`
	DeliveredAt string `json:"delivered_at,omitempty"`
	AckedAt     string `json:"acked_at,omitempty"`
	// Attempts 是已发起的投递次数（含中断的那次）。
	Attempts int `json:"attempts"`
	// AmbiguousAttempts 是「发起了但没记到结果」的次数。它只增不减：
	// 这些次投递可能已经弹过窗，也可能没有，任何后续判定都必须带上这个不确定度。
	AmbiguousAttempts int `json:"ambiguous_attempts"`
	// OpenAttempt 为真表示有一次投递正在进行、或进程死在投递途中还没被 Recover 收尾。
	OpenAttempt bool `json:"open_attempt"`
	// DuplicateRisk 为真表示这条提醒再投一次可能让用户看到第二遍。
	DuplicateRisk bool `json:"duplicate_risk"`
	// Evidence 是最近一次成功投递的外部证据（弹窗句柄、托盘气泡 id），可空。
	// 空证据不等于没投递，只说明只有本包的记录、没有外部旁证。
	Evidence  string `json:"evidence,omitempty"`
	LastError string `json:"last_error,omitempty"`

	// attemptNo／attemptStartedAt 是随 meta_json 一起落库的内部状态：
	// 外部只需要知道「有没有未完成尝试」和「试过几次」，不需要知道是第几号。
	// 但重建 meta 时必须原样带回，否则每次更新都会把尝试序号清成 0，
	// 让 CompleteAttempt 无法判断结果属于哪一次尝试。
	attemptNo        int
	attemptStartedAt string
}

// Handled 报告这条提醒是否已被用户确认。
func (n Notification) Handled() bool { return n.Status.Handled() }

// EventKind 是提醒规则认识的业务事件类型，与事件账本的 kind 对齐。
type EventKind string

const (
	// EventRunPhase 对应 run.phase 事件。
	EventRunPhase EventKind = "run.phase"
	// EventTaskStatus 对应 task.status 事件。
	EventTaskStatus EventKind = "task.status"
	// EventDeliveryReview 对应交付评审结论（maw.delivery.manifest/v1 的 review）。
	EventDeliveryReview EventKind = "delivery.review"
)

// Valid 报告事件类型是否已登记。
func (e EventKind) Valid() bool {
	switch e {
	case EventRunPhase, EventTaskStatus, EventDeliveryReview:
		return true
	}
	return false
}

// BusinessEvent 是提醒规则的输入：一条业务事实，不是一个「通知请求」。
//
// 规则负责判断它值不值得打扰；本包不接受「直接给我弹一条」这种调用，
// 因为那样去重就无从谈起——没有状态词就没有「无变化」。
type BusinessEvent struct {
	// EventID 是业务事件身份，与事件账本同一个命名空间（evt_ 形态）。
	// 它是幂等键的一半：同一事件重放不会产生第二条提醒。
	EventID string
	// ProjectID 是工程身份（16 位十六进制）。
	ProjectID string
	// Kind 是业务事件类型。
	Kind EventKind
	// Subject 是受影响对象的用户视角身份，通常是 ti_xxx。
	Subject string
	// Phase 仅在 Kind==EventRunPhase 时使用。
	Phase model.RunPhase
	// TaskStatus 仅在 Kind==EventTaskStatus 时使用。
	TaskStatus model.TaskStatus
	// Verdict 仅在 Kind==EventDeliveryReview 时使用。
	Verdict model.Verdict
	// Reason 是分类原因（F20），可空。
	Reason string
	// Title／Body 是展示文案，由调用方按语言提供；为空时用 kind+subject 兜底。
	Title string
	Body  string
	// OccurredAt 是业务事实发生时间，只用于记录，不参与去重判定。
	OccurredAt time.Time
}

// State 把三类事件的状态归一成一个词，作为去重指纹的一部分。
func (e BusinessEvent) State() string {
	switch e.Kind {
	case EventRunPhase:
		return string(e.Phase)
	case EventTaskStatus:
		return string(e.TaskStatus)
	case EventDeliveryReview:
		return string(e.Verdict)
	}
	return ""
}

// SkipReason 说明一条渠道为什么没有产生提醒。
type SkipReason string

const (
	// SkipNotNeeded 表示这条业务事实不需要打扰用户（运行正常结束、用户自己取消等）。
	SkipNotNeeded SkipReason = "not_needed"
	// SkipNotConfigured 表示该类型的提醒被策略关掉了。
	SkipNotConfigured SkipReason = "not_configured"
	// SkipDuplicate 表示同一业务事件在同一渠道已经提醒过（重放）。
	SkipDuplicate SkipReason = "duplicate"
	// SkipUnchanged 表示同一对象停在同一状态、且上一条还没被确认。
	SkipUnchanged SkipReason = "unchanged"
	// SkipSettled 表示同一对象同一状态刚被确认过，仍在去重窗口内。
	SkipSettled SkipReason = "settled"
)

// Skip 是一条被压掉的渠道及其原因。原因必须能被看板解释，
// 否则「为什么没提醒」会变成新的黑盒。
type Skip struct {
	Channel        Channel    `json:"channel,omitempty"`
	Reason         SkipReason `json:"reason"`
	NotificationID string     `json:"notification_id,omitempty"`
}

// RaiseResult 是一次登记的结果。
type RaiseResult struct {
	Created []Notification `json:"created,omitempty"`
	Skipped []Skip         `json:"skipped,omitempty"`
}

// Empty 报告这次登记没有产生任何提醒（既没创建也没压掉渠道级判定）。
func (r RaiseResult) Empty() bool { return len(r.Created) == 0 && len(r.Skipped) == 0 }

// DeliveryRequest 是交给投递实现的一条提醒。它自带尝试序号与重复风险，
// 因为 OS 弹窗无法保证只弹一次，实现方必须知道这一次是不是可能重复。
type DeliveryRequest struct {
	NotificationID string  `json:"notification_id"`
	ProjectID      string  `json:"project_id"`
	Kind           Kind    `json:"kind"`
	Channel        Channel `json:"channel"`
	Subject        string  `json:"subject"`
	State          string  `json:"state"`
	Reason         string  `json:"reason,omitempty"`
	Title          string  `json:"title,omitempty"`
	Body           string  `json:"body,omitempty"`
	// Attempt 是从 1 开始的尝试序号。
	Attempt int `json:"attempt"`
	// DuplicateRisk 为真表示这条提醒此前有过中断的投递，用户可能已经见过。
	DuplicateRisk bool `json:"duplicate_risk"`
}

// DeliveryReceipt 是投递实现返回的证据。空证据是允许的，但意味着
// 「投递过」这件事只有本包的记录，没有外部旁证。
type DeliveryReceipt struct {
	Evidence string `json:"evidence,omitempty"`
}

// Deliverer 是投递端口。实现方在接入层（桌面托盘、OS 弹窗、未来的 Web Push）。
//
// 契约：
//
//   - 返回 nil error 只代表投递动作完成，不代表用户看见或处理了；后者只有 Ack 能回答。
//   - 实现必须容忍重复调用：中断后 Recover 会再投一次，届时 DuplicateRisk 为真。
//   - 实现不得在本包里做任何事务性假设——它总是在事务外被调用。
type Deliverer interface {
	Deliver(ctx context.Context, req DeliveryRequest) (DeliveryReceipt, error)
}

// TxRunner 是写事务入口，签名与 db.Store.Write 一致。
// 本包因此不必 import internal/db，也就不会把存储实现钉进语义层。
type TxRunner interface {
	Write(fn func(tx *sql.Tx) error) error
}

// Outcome 是一次投递尝试的结果，由调用方在事务内交回。
type Outcome struct {
	// Delivered 为真表示投递动作成功（不代表用户看见）。
	Delivered bool
	// Ambiguous 为真表示投递实现无法判定用户是否已经看到（弹窗超时、进程被打断、
	// 托盘没回执）。它既不算成功也不算失败：记录留在未处理，
	// 但 AmbiguousAttempts 与 DuplicateRisk 会记下这份不确定。
	Ambiguous bool
	// Evidence 是投递证据，可空。
	Evidence string
	// Err 是失败原因，Delivered 与 Ambiguous 都为假时应当非空。
	Err string
}

// Attempt 是一次投递意图：先记意图、再投递、最后记结果。
type Attempt struct {
	Request DeliveryRequest `json:"request"`
	// No 是本次尝试序号，交回 CompleteAttempt 时必须原样带回。
	No int `json:"attempt"`
}

// RecoveryReport 是重启后的恢复清单。
type RecoveryReport struct {
	// Ambiguous 是「投递意图已记、结果没记」的记录：可能已经弹过窗。
	Ambiguous []Notification `json:"ambiguous,omitempty"`
	// Resumable 是尚未投递成功的未处理提醒（pending／failed，且没有未完成尝试）。
	Resumable []Notification `json:"resumable,omitempty"`
	// AwaitingAck 是已投递成功、等用户确认的提醒。它们不该再弹一次。
	AwaitingAck []Notification `json:"awaiting_ack,omitempty"`
	// Scanned 是本次扫描的未处理记录数。
	Scanned int `json:"scanned"`
	// Truncated 为真表示未处理记录超过扫描上限，本次结果不完整。
	// 不静默截断：宁可让调用方看见「没扫全」，也不要让提醒悄悄消失。
	Truncated bool `json:"truncated"`
}

// PruneReport 是一次保留策略执行的结果。
type PruneReport struct {
	// Cutoff 是本次清理的确认时间下界（早于它的已确认记录被删）。
	Cutoff string `json:"cutoff"`
	// Removed 是被删掉的已确认记录数。
	Removed int64 `json:"removed"`
	// Kept 是清理后该范围内仍在的记录数（含全部未处理记录）。
	Kept int `json:"kept"`
}

// DispatchReport 是一次投递循环的结果。
type DispatchReport struct {
	Delivered int `json:"delivered"`
	// Ambiguous 是投递结果不可判定的记录数：它们仍在未处理列表里，且带重复风险。
	Ambiguous int `json:"ambiguous"`
	Failed    int `json:"failed"`
	// Skipped 是没有投递实现、且不属于站内渠道的记录数。
	Skipped int `json:"skipped"`
	// Handled 是扫描后到投递前被用户确认掉的记录数。
	Handled int `json:"handled"`
}

var (
	// ErrUnknownNotification 表示提醒身份不存在。
	ErrUnknownNotification = errors.New("notifications: 提醒不存在")
	// ErrAlreadyHandled 表示这条提醒已被用户确认，不再需要投递。
	ErrAlreadyHandled = errors.New("notifications: 提醒已确认")
	// ErrAttemptMismatch 表示交回的尝试序号与记录里的不一致：
	// 说明另有一次尝试改过这条记录，本次结果不能覆盖它。
	ErrAttemptMismatch = errors.New("notifications: 尝试序号不匹配")
)
