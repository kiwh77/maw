package model

import (
	"regexp"
	"strings"
)

// 身份类型的唯一解析规则。形态定义来自契约，正则与原校验器逐字一致：
//   - instance/display：task_contract.py INSTANCE_ID_RE / DISPLAY_ID_RE
//   - run/operation/lease/event：runtime_contract.py
//   - delivery/artifact/verification：delivery_contract.py
var (
	reInstanceID     = regexp.MustCompile(`^ti_[a-z0-9]{6,}$`)
	reDisplayID      = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)
	reRunID          = regexp.MustCompile(`^run_[a-z0-9]{3,}$`)
	reOperationID    = regexp.MustCompile(`^op_[a-z0-9]{3,}$`)
	reLeaseID        = regexp.MustCompile(`^ls_[a-z0-9]{3,}$`)
	reEventID        = regexp.MustCompile(`^evt_[a-z0-9]{3,}$`)
	reDeliveryID     = regexp.MustCompile(`^dlv_[a-z0-9]{3,}$`)
	reArtifactID     = regexp.MustCompile(`^art_[a-z0-9]{3,}$`)
	reVerificationID = regexp.MustCompile(`^vfy_[a-z0-9]{3,}$`)
	reProjectID      = regexp.MustCompile(`^[0-9a-f]{16}$`)
	reNodeID         = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
	reAgentID        = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)+$`)
	reCommit         = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	reSHA256         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reSemVer         = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

// ProjectID 是工程稳定 ID（sha1(绝对路径) 前 8 字节的十六进制）。
// 生成权威在 internal/home.ProjectIDFromPath；本包只做形态校验。
type ProjectID string

// InstanceID 是任务实例身份：永不复用，与 display_id 属于两个命名空间（E111）。
type InstanceID string

// DisplayID 是给人看的编号（如 VN-001），同一工程同一时刻至多一个活跃实例持有（E120）。
type DisplayID string

// RunID 是一次执行尝试的身份。
type RunID string

// OperationID 是持久副作用操作的身份。
type OperationID string

// LeaseID 是一次资源占用的身份，释放时必须能引用回它（E261）。
type LeaseID string

// EventID 是一条标准事实的身份（E212）。
type EventID string

// NodeID 是节点身份；NodeSlot 是节点上的可并发位置，属于调度资源。
type NodeID string

// AgentID 是工作者身份。生成权威在 internal/identity.Format（{应用}-{工程slug}-{序号}）。
type AgentID string

// DeliveryID 是一次交付的身份。
type DeliveryID string

// ArtifactID 是一个产物引用的身份。
type ArtifactID string

// VerificationID 是一条验证结论的身份。
type VerificationID string

// Commit 是 git 提交哈希（7–40 位小写十六进制，E313）。
type Commit string

// SHA256 是文件类产物的内容哈希（64 位小写十六进制）。
type SHA256 string

// SemVer 是接入器能力版本（语义化版本 x.y.z）。
type SemVer string

// VersionNo 是任务定义版本号，从 1 连续递增（E131）。
type VersionNo int

// Generation 是 Run 代际，从 1 开始单调递增；旧代际写入一律失效。
type Generation int

// Revision 是任务行的乐观锁版本；条件写入必须比对它。
type Revision int64

// Cursor 是事件入库序号（不是发生顺序），严格递增（E213）。
type Cursor int64

// Valid 报告工程 ID 形态是否合法。
func (v ProjectID) Valid() bool { return reProjectID.MatchString(string(v)) }

// Valid 报告实例身份是否合法。
func (v InstanceID) Valid() bool { return reInstanceID.MatchString(string(v)) }

// Valid 报告显示编号是否合法。
func (v DisplayID) Valid() bool { return reDisplayID.MatchString(string(v)) }

// Valid 报告运行身份是否合法。
func (v RunID) Valid() bool { return reRunID.MatchString(string(v)) }

// Valid 报告操作身份是否合法。
func (v OperationID) Valid() bool { return reOperationID.MatchString(string(v)) }

// Valid 报告占用身份是否合法。
func (v LeaseID) Valid() bool { return reLeaseID.MatchString(string(v)) }

// Valid 报告事件身份是否合法。
func (v EventID) Valid() bool { return reEventID.MatchString(string(v)) }

// Valid 报告节点身份是否合法。
func (v NodeID) Valid() bool { return reNodeID.MatchString(string(v)) }

// Valid 报告工作者身份是否合法（仅拒绝空值、大写、空格与反斜杠等传输层错误）。
func (v AgentID) Valid() bool { return reAgentID.MatchString(string(v)) }

// Valid 报告交付身份是否合法。
func (v DeliveryID) Valid() bool { return reDeliveryID.MatchString(string(v)) }

// Valid 报告产物身份是否合法。
func (v ArtifactID) Valid() bool { return reArtifactID.MatchString(string(v)) }

// Valid 报告验证身份是否合法。
func (v VerificationID) Valid() bool { return reVerificationID.MatchString(string(v)) }

// Valid 报告提交哈希是否合法。
func (v Commit) Valid() bool { return reCommit.MatchString(string(v)) }

// Valid 报告内容哈希是否合法。
func (v SHA256) Valid() bool { return reSHA256.MatchString(string(v)) }

// Valid 报告语义化版本是否合法。
func (v SemVer) Valid() bool { return reSemVer.MatchString(string(v)) }

// Valid 报告版本号是否为正。
func (v VersionNo) Valid() bool { return v >= 1 }

// Valid 报告代际是否为正。
func (v Generation) Valid() bool { return v >= 1 }

// Valid 报告游标是否为正（0 表示尚未写入）。
func (v Cursor) Valid() bool { return v >= 1 }

// ParseInstanceID 解析并校验实例身份；形态非法时返回 E111。
func ParseInstanceID(s string) (InstanceID, error) {
	id := InstanceID(strings.TrimSpace(s))
	if !id.Valid() {
		return "", Violation(CodeInstanceIDShape, "instance_id", "必须形如 ti_<小写字母数字，至少 6 位>")
	}
	return id, nil
}

// ParseDisplayID 解析并校验显示编号；形态非法时返回 E112。
func ParseDisplayID(s string) (DisplayID, error) {
	id := DisplayID(strings.TrimSpace(s))
	if !id.Valid() {
		return "", Violation(CodeDisplayIDShape, "display_id", "必须形如 VN-001 的大写前缀加数字")
	}
	return id, nil
}

// ParseCommit 解析并校验提交哈希；形态非法时返回 E313。
func ParseCommit(s string) (Commit, error) {
	c := Commit(strings.TrimSpace(s))
	if !c.Valid() {
		return "", Violation(CodeCommitRefInvalid, "commit", "必须是 7–40 位小写十六进制")
	}
	return c, nil
}

// ParseSHA256 解析并校验内容哈希。
func ParseSHA256(s string) (SHA256, error) {
	h := SHA256(strings.TrimSpace(s))
	if !h.Valid() {
		return "", Violation(CodeArtifactMissing, "sha256", "必须是 64 位小写十六进制")
	}
	return h, nil
}

// LegacyPrimaryKey 返回旧物理主键 project_id + "/" + display_id（E160 的等式）。
func LegacyPrimaryKey(p ProjectID, d DisplayID) string {
	return string(p) + "/" + string(d)
}
