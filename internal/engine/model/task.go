package model

import (
	"fmt"
	"time"
)

// TaskContract 是任务实例与版本契约的标识（E101）。
const TaskContract = "maw.task.instance/v1"

// DepKind 区分新写入的实例依赖与存量显示编号依赖。
type DepKind string

// 实例依赖（新写入一律用它）／legacy 依赖（只用于读存量 deps_json）。
const (
	DepInstance DepKind = "instance"
	DepLegacy   DepKind = "legacy"
)

// Dep 是一条依赖。Kind=instance 时 Target 是 InstanceID；Kind=legacy 时 Target 是 DisplayID。
type Dep struct {
	Kind   DepKind `json:"kind"`
	Target string  `json:"target"`
}

// TaskVersion 是一次冻结的任务定义；已冻结版本不可改（§4）。
type TaskVersion struct {
	VersionNo VersionNo `json:"version_no"`
	FrozenAt  time.Time `json:"frozen_at"`
	Title     string    `json:"title"`
	Deps      []Dep     `json:"deps"`
}

// InstanceState 是实例身份状态：活跃或已退役。
type InstanceState string

// 活跃／退役。
const (
	InstanceStateActive  InstanceState = "active"
	InstanceStateRetired InstanceState = "retired"
)

// Valid 报告取值是否合法。
func (s InstanceState) Valid() bool {
	return s == InstanceStateActive || s == InstanceStateRetired
}

// LegacyAnchor 把实例摊回 tasks 的物理主键（E160 强制存在）。
type LegacyAnchor struct {
	ProjectID     ProjectID `json:"project_id"`
	DisplayID     DisplayID `json:"display_id"`
	PrimaryKey    string    `json:"primary_key"`
	Ambiguous     bool      `json:"ambiguous"`
	AmbiguityNote *string   `json:"ambiguity_note,omitempty"`
}

// TaskInstance 是一个任务实例的身份快照，不含运行态（运行态只在 tasks 行上）。
type TaskInstance struct {
	InstanceID    InstanceID    `json:"instance_id"`
	DisplayID     DisplayID     `json:"display_id"`
	State         InstanceState `json:"state"`
	CreatedAt     time.Time     `json:"created_at"`
	RetiredAt     *time.Time    `json:"retired_at,omitempty"`
	RetiredReason *string       `json:"retired_reason,omitempty"`
	ActiveVersion *VersionNo    `json:"active_version"`
	Versions      []TaskVersion `json:"versions"`
	Legacy        LegacyAnchor  `json:"legacy"`
}

// TaskSnapshot 是某工程某一时刻的任务身份快照。
type TaskSnapshot struct {
	Contract    string         `json:"contract"`
	ProjectID   ProjectID      `json:"project_id"`
	GeneratedAt time.Time      `json:"generated_at"`
	Instances   []TaskInstance `json:"instances"`
}

// Task 返回指定实例；不存在返回 nil。
func (s TaskSnapshot) Task(id InstanceID) *TaskInstance {
	for i := range s.Instances {
		if s.Instances[i].InstanceID == id {
			return &s.Instances[i]
		}
	}
	return nil
}

// Validate 按 task-instance-contract-v1 §6 的不变量逐条检查，返回全部违反。
func (s TaskSnapshot) Validate() []*Error {
	var out []*Error
	if s.Contract != TaskContract {
		out = append(out, Violationf(CodeTaskContractUnsupported, "contract",
			"必须是 %s，收到 %q", TaskContract, s.Contract))
	}
	if !s.ProjectID.Valid() {
		out = append(out, Violation(CodeTaskFieldInvalid, "project_id", "必须是 16 位小写十六进制"))
	}
	if s.GeneratedAt.IsZero() {
		out = append(out, Violation(CodeTaskFieldInvalid, "generated_at", "必须是可解析的 ISO8601 时间"))
	}

	byInstance := make(map[InstanceID]*TaskInstance, len(s.Instances))
	activeByDisplay := make(map[DisplayID]InstanceID, len(s.Instances))
	displayHits := make(map[DisplayID][]*TaskInstance, len(s.Instances))

	for i := range s.Instances {
		inst := &s.Instances[i]
		p := fmt.Sprintf("instances[%d]", i)
		if _, dup := byInstance[inst.InstanceID]; dup {
			out = append(out, Violationf(CodeInstanceDuplicate, p+".instance_id", "%q 重复", inst.InstanceID))
		} else if inst.InstanceID.Valid() {
			byInstance[inst.InstanceID] = inst
		}
		displayHits[inst.DisplayID] = append(displayHits[inst.DisplayID], inst)

		out = append(out, inst.validate(p, s.ProjectID)...)

		if inst.State == InstanceStateActive {
			if prev, dup := activeByDisplay[inst.DisplayID]; dup {
				out = append(out, Violationf(CodeActiveAliasConflict, p+".display_id",
					"%s 已由活跃实例 %s 持有", inst.DisplayID, prev))
			} else {
				activeByDisplay[inst.DisplayID] = inst.InstanceID
			}
		}
	}

	// 依赖解析（§5）：先 instance 精确匹配，再 legacy 在当前工程内匹配。
	for i := range s.Instances {
		inst := &s.Instances[i]
		for vi := range inst.Versions {
			ver := &inst.Versions[vi]
			for di, dep := range ver.Deps {
				path := fmt.Sprintf("instances[%d].versions[%d].deps[%d]", i, vi, di)
				out = append(out, resolveDep(inst, dep, path, byInstance, displayHits, s.ProjectID)...)
			}
		}
	}
	return out
}

func resolveDep(self *TaskInstance, dep Dep, path string, byInstance map[InstanceID]*TaskInstance, displayHits map[DisplayID][]*TaskInstance, project ProjectID) []*Error {
	switch dep.Kind {
	case DepInstance:
		target, ok := byInstance[InstanceID(dep.Target)]
		if !ok {
			return []*Error{Violationf(CodeDepMissing, path, "实例 %q 不在本工程快照内", dep.Target)}
		}
		if target.InstanceID == self.InstanceID {
			return []*Error{Violation(CodeDepSelf, path, "不得依赖自身实例")}
		}
		if target.State == InstanceStateRetired {
			return []*Error{Violationf(CodeDepRetired, path, "%s 已退役，依赖不可满足", target.InstanceID)}
		}
		return nil

	case DepLegacy:
		hits := displayHits[DisplayID(dep.Target)]
		switch {
		case len(hits) == 0:
			return []*Error{Violationf(CodeDepMissing, path, "显示编号 %q 在本工程内不存在", dep.Target)}
		case len(hits) > 1:
			return []*Error{Violationf(CodeDepAmbiguous, path,
				"显示编号 %q 命中 %d 个实例（含退役锚点），必须改用 instance 形式消歧", dep.Target, len(hits))}
		}
		target := hits[0]
		if target.Legacy.ProjectID != "" && target.Legacy.ProjectID != project {
			return []*Error{Violationf(CodeDepMissing, path, "%q 属于其它工程", dep.Target)}
		}
		if target.InstanceID == self.InstanceID {
			return []*Error{Violation(CodeDepSelf, path, "不得依赖自身实例")}
		}
		if target.State == InstanceStateRetired {
			return []*Error{Violationf(CodeDepRetired, path, "%s 已退役，依赖不可满足", target.DisplayID)}
		}
		return nil

	default:
		return []*Error{Violationf(CodeTaskFieldInvalid, path+".kind", "只能是 instance 或 legacy，收到 %q", dep.Kind)}
	}
}

func (inst *TaskInstance) validate(p string, project ProjectID) []*Error {
	var out []*Error
	if !inst.InstanceID.Valid() {
		out = append(out, Violation(CodeInstanceIDShape, p+".instance_id",
			`必须形如 ti_<小写字母数字，至少 6 位>，不得占用显示编号形态`))
	}
	if !inst.DisplayID.Valid() {
		out = append(out, Violation(CodeDisplayIDShape, p+".display_id", "必须形如 VN-001 的大写前缀加数字"))
	}
	if !inst.State.Valid() {
		out = append(out, Violationf(CodeTaskFieldInvalid, p+".state", "只能是 active 或 retired，收到 %q", inst.State))
	}
	if inst.CreatedAt.IsZero() {
		out = append(out, Violation(CodeTaskFieldInvalid, p+".created_at", "必须是可解析的 ISO8601 时间"))
	}

	out = append(out, validateVersions(p, inst.Versions)...)

	switch inst.State {
	case InstanceStateActive:
		if inst.RetiredAt != nil || inst.RetiredReason != nil {
			out = append(out, Violation(CodeTaskFieldInvalid, p, "active 实例不得带退役字段"))
		}
		if inst.ActiveVersion == nil {
			out = append(out, Violation(CodeActiveVersionDangling, p+".active_version", "active 实例必须指向一个已冻结版本"))
		} else if !hasVersion(inst.Versions, *inst.ActiveVersion) {
			out = append(out, Violationf(CodeActiveVersionDangling, p+".active_version",
				"版本 %d 未冻结", *inst.ActiveVersion))
		} else if max := maxVersion(inst.Versions); *inst.ActiveVersion != max {
			out = append(out, Violationf(CodeActiveVersionRollback, p+".active_version",
				"必须等于已冻结的最大版本 %d，收到 %d", max, *inst.ActiveVersion))
		}

	case InstanceStateRetired:
		if inst.RetiredAt == nil {
			out = append(out, Violation(CodeRetiredIncomplete, p+".retired_at", "退役实例必须写退役时间"))
		}
		if inst.RetiredReason == nil || *inst.RetiredReason == "" {
			out = append(out, Violation(CodeRetiredIncomplete, p+".retired_reason", "退役实例必须写退役原因"))
		}
		if inst.ActiveVersion != nil {
			out = append(out, Violation(CodeRetiredIncomplete, p+".active_version", "退役实例的 active_version 必须为 null"))
		}
		if inst.RetiredAt != nil {
			for vi, v := range inst.Versions {
				if v.FrozenAt.After(*inst.RetiredAt) {
					out = append(out, Violationf(CodeFrozenAfterRetire,
						fmt.Sprintf("%s.versions[%d].frozen_at", p, vi),
						"退役时间 %s 之后不得再冻结版本", inst.RetiredAt.Format(time.RFC3339)))
				}
			}
		}
	}

	// legacy 锚点（E160、E161）。
	want := LegacyPrimaryKey(project, inst.DisplayID)
	if inst.Legacy.ProjectID != project || inst.Legacy.DisplayID != inst.DisplayID || inst.Legacy.PrimaryKey != want {
		out = append(out, Violationf(CodeLegacyAnchorMissing, p+".legacy",
			"必须存在且 primary_key == %q", want))
	}
	if inst.Legacy.Ambiguous {
		if inst.State == InstanceStateActive {
			out = append(out, Violation(CodeLegacyAmbiguousActive, p+".legacy", "歧义历史不得作为活跃实例"))
		}
		if inst.Legacy.AmbiguityNote == nil || *inst.Legacy.AmbiguityNote == "" {
			out = append(out, Violation(CodeLegacyAmbiguousActive, p+".legacy.ambiguity_note", "ambiguous=true 时必须写明说明"))
		}
	}
	return out
}

func validateVersions(p string, versions []TaskVersion) []*Error {
	var out []*Error
	if len(versions) == 0 {
		return append(out, Violation(CodeTaskFieldInvalid, p+".versions", "至少冻结一个版本"))
	}
	seen := make(map[VersionNo]bool, len(versions))
	for i, v := range versions {
		vp := fmt.Sprintf("%s.versions[%d]", p, i)
		if want := VersionNo(i + 1); v.VersionNo != want {
			out = append(out, Violationf(CodeVersionSequence, vp+".version_no",
				"必须从 1 连续递增，位置 %d 应为 %d，收到 %d", i, want, v.VersionNo))
		}
		if seen[v.VersionNo] {
			out = append(out, Violationf(CodeVersionSequence, vp+".version_no", "版本号 %d 重复", v.VersionNo))
		}
		seen[v.VersionNo] = true
		if v.FrozenAt.IsZero() {
			out = append(out, Violation(CodeTaskFieldInvalid, vp+".frozen_at", "必须是可解析的 ISO8601 时间"))
		}
		if i > 0 && !v.FrozenAt.IsZero() && !versions[i-1].FrozenAt.IsZero() &&
			!v.FrozenAt.After(versions[i-1].FrozenAt) {
			out = append(out, Violation(CodeVersionFrozenOrder, vp+".frozen_at", "冻结时间必须严格晚于上一版本"))
		}
	}
	return out
}

func hasVersion(versions []TaskVersion, n VersionNo) bool {
	for _, v := range versions {
		if v.VersionNo == n {
			return true
		}
	}
	return false
}

func maxVersion(versions []TaskVersion) VersionNo {
	var max VersionNo
	for _, v := range versions {
		if v.VersionNo > max {
			max = v.VersionNo
		}
	}
	return max
}

// ActiveVersionOf 返回活跃实例当前生效的定义版本；非活跃返回 nil。
func (inst *TaskInstance) ActiveVersionOf() *TaskVersion {
	if inst.State != InstanceStateActive || inst.ActiveVersion == nil {
		return nil
	}
	for i := range inst.Versions {
		if inst.Versions[i].VersionNo == *inst.ActiveVersion {
			return &inst.Versions[i]
		}
	}
	return nil
}
