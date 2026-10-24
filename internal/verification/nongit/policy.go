package nongit

import (
	"github.com/kiwh77/maw/internal/engine/model"
)

// Action 是一次验收动作。
type Action string

const (
	// ActionEvaluate 只检查产物条件与旧证据时效，不写接受／拒绝结论。
	ActionEvaluate Action = "evaluate"
	// ActionAccept 在条件满足时给出人工接受结论（可支撑 done）。
	ActionAccept Action = "accept"
	// ActionReject 给出人工拒绝结论；必须带原因（E360）。
	ActionReject Action = "reject"
)

// Condition 是一条类型化产物条件（冻结在任务／工程策略里，不是模型输出）。
type Condition struct {
	ArtifactID model.ArtifactID
	// Kind 限定 file 或 content_hash；非代码不验收 commit。
	Kind model.ArtifactKind
	// ExpectedSHA256 非空时要求当前产物哈希一致。
	ExpectedSHA256 model.SHA256
}

// Policy 是非代码验收策略。
type Policy struct {
	Conditions []Condition
	// Checklist 是须全部勾选的验收条件条目。
	Checklist []string
}

// Target 是被验收对象：当前产物快照 + 既有验证证据。
type Target struct {
	TaskVersion model.VersionNo
	Artifacts   []model.Artifact
	// Prior 是已有验证；产物哈希漂移时会被复制为 stale 并作为独立证据返回。
	Prior []model.Verification
}

// Request 是一次验收请求。
type Request struct {
	Policy Policy
	Target Target
	Action Action
	// Reviewer 是人工验收人；Accept 必填。
	Reviewer string
	// Note 是接受时的结果类证据（核对说明／清单摘要），不得仅 exit_code。
	Note string
	// Reason 是拒绝原因；Reject 必填。
	Reason string
	// Satisfied 是本次勾选的 checklist 条目；Accept 时须覆盖 Policy.Checklist。
	Satisfied []string
}

// Outcome 是一次验收结论与证据。
type Outcome struct {
	Verification model.Verification
	// Stale 是因产物变更而失效的旧验证副本（Result=stale），与新证据并列保留。
	Stale []model.Verification
	Review     model.Review
	Merge      model.Merge // 恒为跳过合入
	Completion model.Completion
	SupportsDone bool
	ConditionsOK bool
	// Missing 列出未满足条件（缺产物／哈希不符／清单未勾等）。
	Missing []string
	Reason  string
}
