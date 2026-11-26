package retention

import (
	"fmt"
	"time"
)

// Decision 是对候选的保留／清理判定。
type Decision struct {
	Action  Action              `json:"action"`
	Reasons []ProtectionReason  `json:"reasons,omitempty"`
	Policy  Policy              `json:"-"`
}

// Evaluate 按显式策略与占用／证据护栏判定；保护优先于年龄。
//
// 无强删旁路：即使 Age 超过上限，Occupied／Unknown／PendingJournal／DeliveryEvidence
// 仍强制 retain。
func Evaluate(p Policy, c Candidate, g RunGuard) Decision {
	var reasons []ProtectionReason

	if p.ProtectOccupied && g.Occupies {
		reasons = append(reasons, ReasonOccupied)
	}
	if p.ProtectUnknown && g.ReconciliationUnknown {
		reasons = append(reasons, ReasonUnknown)
	}
	if p.ProtectPendingJournal && (c.Kind == TargetJournal || c.Kind == TargetRunDir || c.Kind == TargetLog) {
		if g.PendingJournal > 0 {
			reasons = append(reasons, ReasonPendingJournal)
		}
	}
	if p.ProtectDeliveryEvidence && (c.Referenced || g.DeliveryRefs > 0 || c.Kind == TargetArtifact && c.Referenced) {
		reasons = append(reasons, ReasonDeliveryEvidence)
	}

	if len(reasons) > 0 {
		return Decision{Action: ActionRetain, Reasons: reasons, Policy: p}
	}

	maxAge := maxAgeFor(p, c.Kind)
	if maxAge <= 0 {
		// 未配置该类别时限 → 保留（显式：0 表示不按时间淘汰）
		return Decision{Action: ActionRetain, Reasons: []ProtectionReason{ReasonWithinRetention}, Policy: p}
	}
	if c.Age < maxAge {
		return Decision{Action: ActionRetain, Reasons: []ProtectionReason{ReasonNotAged}, Policy: p}
	}
	// 进行中未终态的运行目录即使「看起来不占用」也保守保留
	if c.Kind == TargetRunDir && !g.Terminal {
		return Decision{Action: ActionRetain, Reasons: []ProtectionReason{ReasonOccupied}, Policy: p}
	}
	return Decision{Action: ActionPurge, Policy: p}
}

func maxAgeFor(p Policy, kind TargetKind) time.Duration {
	switch kind {
	case TargetLog, TargetJournal:
		return p.LogMaxAge
	case TargetArtifact:
		return p.ArtifactMaxAge
	case TargetRunDir:
		return p.RunDirMaxAge
	default:
		return 0
	}
}

// Explain 返回人类可读的保护原因（测试／CLI 共用）。
func (d Decision) Explain() string {
	if d.Action == ActionPurge {
		return "purge"
	}
	if len(d.Reasons) == 0 {
		return "retain"
	}
	return fmt.Sprintf("retain:%v", d.Reasons)
}
