package retention

import "time"

// TargetKind 是可纳入保留策略的对象类别。
type TargetKind string

const (
	TargetRunDir   TargetKind = "run_dir"
	TargetLog      TargetKind = "log"
	TargetJournal  TargetKind = "journal"
	TargetArtifact TargetKind = "artifact"
)

// Valid 报告类别是否封闭。
func (k TargetKind) Valid() bool {
	switch k {
	case TargetRunDir, TargetLog, TargetJournal, TargetArtifact:
		return true
	}
	return false
}

// Policy 是显式默认保留策略（架构 §10.4：大小／时间／总量限额由配置表达）。
type Policy struct {
	// LogMaxAge 诊断日志最长保留；0 表示不按时间淘汰日志。
	LogMaxAge time.Duration `json:"log_max_age"`
	// ArtifactMaxAge 内容哈希产物最长保留；仍被交付引用时不受此限。
	ArtifactMaxAge time.Duration `json:"artifact_max_age"`
	// RunDirMaxAge 已结束且无占用的运行目录最长保留。
	RunDirMaxAge time.Duration `json:"run_dir_max_age"`

	// ProtectOccupied 保护仍占用资源的运行目录（默认 true，不可在 Default 中关闭语义）。
	ProtectOccupied bool `json:"protect_occupied"`
	// ProtectUnknown 保护 reconciliation=unknown 的现场（默认 true；无强删旁路）。
	ProtectUnknown bool `json:"protect_unknown"`
	// ProtectPendingJournal 保护仍有待补交记录的 journal（默认 true）。
	ProtectPendingJournal bool `json:"protect_pending_journal"`
	// ProtectDeliveryEvidence 保护仍被未完成／待验收交付引用的唯一产物（默认 true）。
	ProtectDeliveryEvidence bool `json:"protect_delivery_evidence"`
}

// DefaultPolicy 返回显式默认值：14 天日志／产物／目录，四类保护全开。
func DefaultPolicy() Policy {
	return Policy{
		LogMaxAge:               14 * 24 * time.Hour,
		ArtifactMaxAge:          14 * 24 * time.Hour,
		RunDirMaxAge:            14 * 24 * time.Hour,
		ProtectOccupied:         true,
		ProtectUnknown:          true,
		ProtectPendingJournal:   true,
		ProtectDeliveryEvidence: true,
	}
}

// ProtectionReason 说明为何不可回收。
type ProtectionReason string

const (
	ReasonOccupied         ProtectionReason = "occupied"
	ReasonUnknown          ProtectionReason = "unknown_reconciliation"
	ReasonPendingJournal   ProtectionReason = "pending_journal"
	ReasonDeliveryEvidence ProtectionReason = "delivery_evidence"
	ReasonWithinRetention  ProtectionReason = "within_retention"
	ReasonNotAged          ProtectionReason = "not_aged"
)

// Action 是评估结论。
type Action string

const (
	ActionRetain Action = "retain"
	ActionPurge  Action = "purge"
)

// RunGuard 是评估时只读的运行占用／证据摘要（由调用方从 db／runner 填入）。
type RunGuard struct {
	RunID                string
	Occupies             bool
	ReconciliationUnknown bool
	ProcessStopConfirmed bool
	PendingJournal       int
	// DeliveryRefs 仍引用该路径／产物的未完成交付数。
	DeliveryRefs int
	// Terminal 阶段是否已终态（completed／failed／cancelled／timed_out）。
	Terminal bool
}

// Candidate 是一条清理候选。
type Candidate struct {
	Kind      TargetKind
	Path      string
	RunID     string
	Artifact  string // 可选 content-hash
	Age       time.Duration
	Bytes     int64
	Referenced bool // 仍被交付／验收引用
}
