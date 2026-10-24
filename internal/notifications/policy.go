package notifications

import (
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// 默认时长。都是「提醒」这一层的量级，不是业务状态机的量级。
const (
	// DefaultDedupWindow 是「已确认提醒」的去重窗口：同对象同状态在这个窗口内
	// 再次出现，说明用户刚处理过，不再打扰。窗口外再次发生算新的一次介入需求。
	DefaultDedupWindow = 30 * time.Minute
	// DefaultRetentionAcked 是已确认提醒的保留期。
	DefaultRetentionAcked = 30 * 24 * time.Hour
)

// Rule 是一种提醒类型的投递配置。Channels 为空表示关闭该类型。
type Rule struct {
	Channels []Channel `json:"channels"`
}

// Policy 是提醒规则的可配置部分（F33／F47）。
//
// Rules 为 nil 时用默认策略；显式给了 Rules 时，未列出的类型视为关闭——
// 配置一旦落地就以配置为准，不再偷偷补默认值。
type Policy struct {
	Rules          map[Kind]Rule `json:"rules,omitempty"`
	DedupWindow    time.Duration `json:"dedup_window,omitempty"`
	RetentionAcked time.Duration `json:"retention_acked,omitempty"`
}

// DefaultPolicy 返回默认策略：等人与失败走站内加桌面，交付变化只走站内。
//
// 交付变化（待验收、验收通过、任务完成）频率高、单条价值低，默认不弹 OS 窗；
// 等人与失败是真正卡住流程的事，值得打断用户。
func DefaultPolicy() Policy {
	return Policy{
		Rules: map[Kind]Rule{
			KindWaiting:  {Channels: []Channel{ChannelInApp, ChannelDesktop}},
			KindFailed:   {Channels: []Channel{ChannelInApp, ChannelDesktop}},
			KindDelivery: {Channels: []Channel{ChannelInApp}},
		},
		DedupWindow:    DefaultDedupWindow,
		RetentionAcked: DefaultRetentionAcked,
	}
}

// normalize 补齐零值：只补时长，不补被显式关掉的类型。
func (p Policy) normalize() Policy {
	if p.DedupWindow <= 0 {
		p.DedupWindow = DefaultDedupWindow
	}
	if p.RetentionAcked <= 0 {
		p.RetentionAcked = DefaultRetentionAcked
	}
	if p.Rules == nil {
		p.Rules = DefaultPolicy().Rules
	}
	return p
}

// Validate 检查配置本身是否可用：类型与渠道必须在封闭集合内。
func (p Policy) Validate() []*model.Error {
	p = p.normalize()
	var out []*model.Error
	for kind, rule := range p.Rules {
		if !kind.Valid() {
			out = append(out, model.Violationf(model.CodePortFieldInvalid,
				"policy.rules", "未登记的提醒类型 %q", kind))
			continue
		}
		seen := map[Channel]bool{}
		for _, ch := range rule.Channels {
			if !ch.Valid() {
				out = append(out, model.Violationf(model.CodePortFieldInvalid,
					"policy.rules."+string(kind), "未登记的渠道 %q", ch))
				continue
			}
			if seen[ch] {
				out = append(out, model.Violationf(model.CodePortFieldInvalid,
					"policy.rules."+string(kind), "渠道 %q 重复配置", ch))
			}
			seen[ch] = true
		}
	}
	return out
}

// Channels 返回某类型实际生效的渠道；关闭时返回 nil。
func (p Policy) Channels(kind Kind) []Channel {
	p = p.normalize()
	rule, ok := p.Rules[kind]
	if !ok {
		return nil
	}
	return append([]Channel(nil), rule.Channels...)
}

// Without 返回一份把某类型关掉的策略副本，便于配置层做局部覆盖。
func (p Policy) Without(kind Kind) Policy {
	p = p.normalize()
	out := p.cloneRules()
	delete(out, kind)
	p.Rules = out
	return p
}

// With 返回一份把某类型改成指定渠道的策略副本；不给渠道即关闭。
func (p Policy) With(kind Kind, channels ...Channel) Policy {
	p = p.normalize()
	out := p.cloneRules()
	if len(channels) == 0 {
		delete(out, kind)
	} else {
		out[kind] = Rule{Channels: append([]Channel(nil), channels...)}
	}
	p.Rules = out
	return p
}

func (p Policy) cloneRules() map[Kind]Rule {
	out := make(map[Kind]Rule, len(p.Rules))
	for k, v := range p.Rules {
		out[k] = Rule{Channels: append([]Channel(nil), v.Channels...)}
	}
	return out
}

// Classify 把一条业务事实翻成提醒类型。第二个返回值为 false 表示「不该打扰」。
//
// 判据只有一条：这件事是不是卡在用户手上。运行正常结束、用户自己取消、
// 评审结论是「不需要评审」都不卡人，因此不提醒；「运行结束」本身也不提醒，
// 因为真正需要动作的是随之而来的交付待验收（F28 把两者分开正是这个意思）。
func Classify(ev BusinessEvent) (Kind, bool) {
	switch ev.Kind {
	case EventRunPhase:
		switch ev.Phase {
		case model.PhaseWaiting:
			return KindWaiting, true
		case model.PhaseFailed, model.PhaseTimedOut:
			return KindFailed, true
		}
	case EventTaskStatus:
		switch ev.TaskStatus {
		case model.TaskBlocked, model.TaskConflict:
			return KindWaiting, true
		case model.TaskInReview:
			return KindDelivery, true
		case model.TaskDone:
			return KindDelivery, true
		}
	case EventDeliveryReview:
		switch ev.Verdict {
		case model.VerdictApproved, model.VerdictChangesRequired:
			return KindDelivery, true
		}
	}
	return "", false
}

// fingerprint 是「同一对象停在同一状态」的判定键。
//
// 只含类型、对象、状态三项：事件身份与发生时间都不进来，否则每次重放都是新指纹，
// 去重形同虚设。工程不进来，因为查库本来就按工程过滤。
func fingerprint(kind Kind, subject, state string) string {
	return strings.Join([]string{string(kind), subject, state}, "|")
}

// ValidateEvent 检查一条业务事实是否足以参与去重判定。
//
// Subject 必填是关键：没有对象就没有「状态未变」的概念，
// 那类事件只能退化成「每来一次弹一次」。
func ValidateEvent(ev BusinessEvent) []*model.Error {
	var out []*model.Error
	if strings.TrimSpace(ev.EventID) == "" {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "event.event_id", "业务事件身份必填"))
	}
	if !model.ProjectID(strings.TrimSpace(ev.ProjectID)).Valid() {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "event.project_id",
			"必须是 16 位十六进制工程身份"))
	}
	if !ev.Kind.Valid() {
		out = append(out, model.Violationf(model.CodePortFieldInvalid, "event.kind",
			"未登记的业务事件类型 %q", ev.Kind))
		return out
	}
	if strings.TrimSpace(ev.Subject) == "" {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "event.subject",
			"受影响对象必填：没有对象就没有「状态未变」可判"))
	}
	switch ev.Kind {
	case EventRunPhase:
		if !ev.Phase.Valid() {
			out = append(out, model.Violationf(model.CodePortFieldInvalid, "event.phase",
				"未知运行阶段 %q", ev.Phase))
		}
	case EventTaskStatus:
		if !ev.TaskStatus.Valid() {
			out = append(out, model.Violationf(model.CodePortFieldInvalid, "event.task_status",
				"未知任务状态 %q", ev.TaskStatus))
		}
	case EventDeliveryReview:
		if !ev.Verdict.Valid() {
			out = append(out, model.Violationf(model.CodePortFieldInvalid, "event.verdict",
				"未知评审结论 %q", ev.Verdict))
		}
	}
	return out
}
