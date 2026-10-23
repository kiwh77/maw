package model_test

import (
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
)

func validTaskSnapshot(t *testing.T) model.TaskSnapshot {
	t.Helper()
	return model.TaskSnapshot{
		Contract:    model.TaskContract,
		ProjectID:   testProject,
		GeneratedAt: mustTime(t, "2026-10-23T02:55:42Z"),
		Instances: []model.TaskInstance{
			{
				InstanceID:    "ti_vn001a",
				DisplayID:     "VN-001",
				State:         model.InstanceStateActive,
				CreatedAt:     mustTime(t, "2026-10-23T02:50:00Z"),
				ActiveVersion: ptr(model.VersionNo(1)),
				Versions: []model.TaskVersion{{
					VersionNo: 1,
					FrozenAt:  mustTime(t, "2026-10-23T02:50:00Z"),
					Title:     "冻结任务实例与版本契约",
				}},
				Legacy: model.LegacyAnchor{
					ProjectID:  testProject,
					DisplayID:  "VN-001",
					PrimaryKey: model.LegacyPrimaryKey(testProject, "VN-001"),
				},
			},
		},
	}
}

func retiredInstance(t *testing.T, id model.InstanceID, display model.DisplayID, frozen, retired string) model.TaskInstance {
	t.Helper()
	reason := "removed_from_plan"
	return model.TaskInstance{
		InstanceID:    id,
		DisplayID:     display,
		State:         model.InstanceStateRetired,
		CreatedAt:     mustTime(t, "2026-10-20T00:00:00Z"),
		RetiredAt:     ptr(mustTime(t, retired)),
		RetiredReason: &reason,
		Versions: []model.TaskVersion{{
			VersionNo: 1,
			FrozenAt:  mustTime(t, frozen),
			Title:     "旧定义",
			Deps:      []model.Dep{},
		}},
		Legacy: model.LegacyAnchor{
			ProjectID:  testProject,
			DisplayID:  display,
			PrimaryKey: model.LegacyPrimaryKey(testProject, display),
		},
	}
}

func TestTaskSnapshotMinimalIsValid(t *testing.T) {
	s := validTaskSnapshot(t)
	assertClean(t, "最小快照", s.Validate())
	if got := s.Task("ti_vn001a"); got == nil || got.DisplayID != "VN-001" {
		t.Fatalf("按实例查身份失败：%+v", got)
	}
	if got := s.Task("ti_vn001a").ActiveVersionOf(); got == nil || got.Title == "" {
		t.Fatalf("活跃版本应可解析：%+v", got)
	}
}

func TestTaskVersionHistory(t *testing.T) {
	s := validTaskSnapshot(t)
	s.Instances[0].Versions = append(s.Instances[0].Versions, model.TaskVersion{
		VersionNo: 2,
		FrozenAt:  mustTime(t, "2026-10-23T03:10:00Z"),
		Title:     "更新后的定义",
	})
	s.Instances[0].ActiveVersion = ptr(model.VersionNo(2))
	assertClean(t, "版本历史前进", s.Validate())
	if got := s.Instances[0].ActiveVersionOf(); got == nil || got.VersionNo != 2 {
		t.Fatalf("活跃版本应指向 v2：%+v", got)
	}
}

func TestVersionSequenceGap(t *testing.T) {
	s := validTaskSnapshot(t)
	s.Instances[0].Versions[0].VersionNo = 2 // 从 2 开始，跳号
	assertHas(t, "版本跳号", s.Validate(), model.CodeVersionSequence)
}

func TestActiveVersionCannotRollBack(t *testing.T) {
	s := validTaskSnapshot(t)
	s.Instances[0].Versions = append(s.Instances[0].Versions, model.TaskVersion{
		VersionNo: 2,
		FrozenAt:  mustTime(t, "2026-10-23T03:10:00Z"),
		Title:     "v2",
	})
	// 指针仍停在 v1：必须等于最大版本（E132）。
	assertHas(t, "活跃指针回退", s.Validate(), model.CodeActiveVersionRollback)
}

func TestFrozenAtMustIncrease(t *testing.T) {
	s := validTaskSnapshot(t)
	s.Instances[0].Versions = append(s.Instances[0].Versions, model.TaskVersion{
		VersionNo: 2,
		FrozenAt:  mustTime(t, "2026-10-23T02:49:00Z"),
		Title:     "时间倒退",
	})
	s.Instances[0].ActiveVersion = ptr(model.VersionNo(2))
	assertHas(t, "冻结时间倒序", s.Validate(), model.CodeVersionFrozenOrder)
}

func TestRetiredInstanceRules(t *testing.T) {
	s := validTaskSnapshot(t)
	r := retiredInstance(t, "ti_vn005a", "VN-005", "2026-10-20T00:00:00Z", "2026-10-21T00:00:00Z")
	s.Instances = append(s.Instances, r)
	assertClean(t, "退役锚点", s.Validate())

	// E140：退役实例不得仍持活跃指针。
	bad := retiredInstance(t, "ti_vn006a", "VN-006", "2026-10-20T00:00:00Z", "2026-10-21T00:00:00Z")
	bad.ActiveVersion = ptr(model.VersionNo(1))
	s.Instances = append(s.Instances, bad)
	assertHas(t, "退役仍持活跃指针", s.Validate(), model.CodeRetiredIncomplete)

	// E142：退役之后不得再冻结版本。
	s2 := validTaskSnapshot(t)
	r2 := retiredInstance(t, "ti_vn007a", "VN-007", "2026-10-20T00:00:00Z", "2026-10-21T00:00:00Z")
	r2.Versions = append(r2.Versions, model.TaskVersion{
		VersionNo: 2,
		FrozenAt:  mustTime(t, "2026-10-22T00:00:00Z"),
		Title:     "退役后新版本",
	})
	s2.Instances = append(s2.Instances, r2)
	assertHas(t, "退役后冻结", s2.Validate(), model.CodeFrozenAfterRetire)
}

func TestDisplayIDIsHeldByOneActiveInstance(t *testing.T) {
	s := validTaskSnapshot(t)
	dup := validTaskSnapshot(t).Instances[0]
	dup.InstanceID = "ti_vn001b" // 同 display_id 的第二个活跃实例
	s.Instances = append(s.Instances, dup)
	assertHas(t, "活跃别名冲突", s.Validate(), model.CodeActiveAliasConflict)
}

func TestDisplayIDRebuildAfterRetire(t *testing.T) {
	s := validTaskSnapshot(t)
	old := retiredInstance(t, "ti_vn001old", "VN-005", "2026-10-20T00:00:00Z", "2026-10-21T00:00:00Z")
	fresh := validTaskSnapshot(t).Instances[0]
	fresh.InstanceID = "ti_vn005b"
	fresh.DisplayID = "VN-005"
	fresh.Legacy = model.LegacyAnchor{
		ProjectID:  testProject,
		DisplayID:  "VN-005",
		PrimaryKey: model.LegacyPrimaryKey(testProject, "VN-005"),
	}
	s.Instances = append(s.Instances, old, fresh)
	assertClean(t, "编号重建", s.Validate())
}

func TestInstanceIdentityShape(t *testing.T) {
	s := validTaskSnapshot(t)
	s.Instances[0].InstanceID = "VN-001" // 实例身份写成了显示编号
	assertHas(t, "实例身份混用", s.Validate(), model.CodeInstanceIDShape)

	s2 := validTaskSnapshot(t)
	s2.Instances[0].DisplayID = "ti_abc123"
	assertHas(t, "显示编号混用", s2.Validate(), model.CodeDisplayIDShape)
}

func TestLegacyAnchorMustMatchPrimaryKey(t *testing.T) {
	s := validTaskSnapshot(t)
	s.Instances[0].Legacy.PrimaryKey = "716c30878ba1b95f/VN-999"
	assertHas(t, "旧主键错位", s.Validate(), model.CodeLegacyAnchorMissing)
}

func TestAmbiguousLegacyCannotBeActive(t *testing.T) {
	s := validTaskSnapshot(t)
	note := "同名历史"
	s.Instances[0].Legacy.Ambiguous = true
	s.Instances[0].Legacy.AmbiguityNote = &note
	assertHas(t, "歧义历史充当活跃别名", s.Validate(), model.CodeLegacyAmbiguousActive)
}

func TestDependencyResolution(t *testing.T) {
	// E150：悬空依赖。
	s := validTaskSnapshot(t)
	s.Instances[0].Versions[0].Deps = []model.Dep{{Kind: model.DepInstance, Target: "ti_missing"}}
	assertHas(t, "悬空依赖", s.Validate(), model.CodeDepMissing)

	// E152：自依赖。
	s2 := validTaskSnapshot(t)
	s2.Instances[0].Versions[0].Deps = []model.Dep{{Kind: model.DepInstance, Target: "ti_vn001a"}}
	assertHas(t, "自依赖", s2.Validate(), model.CodeDepSelf)

	// E151：依赖已退役实例。
	s3 := validTaskSnapshot(t)
	s3.Instances = append(s3.Instances, retiredInstance(t, "ti_vn004a", "VN-004", "2026-10-20T00:00:00Z", "2026-10-21T00:00:00Z"))
	s3.Instances[0].Versions[0].Deps = []model.Dep{{Kind: model.DepInstance, Target: "ti_vn004a"}}
	assertHas(t, "依赖退役前置", s3.Validate(), model.CodeDepRetired)

	// E153：legacy 依赖命中多个实例（活跃 + 退役锚点），必须消歧。
	s4 := validTaskSnapshot(t)
	old := retiredInstance(t, "ti_vn002old", "VN-002", "2026-10-20T00:00:00Z", "2026-10-21T00:00:00Z")
	live := validTaskSnapshot(t).Instances[0]
	live.InstanceID = "ti_vn002b"
	live.DisplayID = "VN-002"
	live.Legacy = model.LegacyAnchor{
		ProjectID:  testProject,
		DisplayID:  "VN-002",
		PrimaryKey: model.LegacyPrimaryKey(testProject, "VN-002"),
	}
	s4.Instances = append(s4.Instances, old, live)
	s4.Instances[0].Versions[0].Deps = []model.Dep{{Kind: model.DepLegacy, Target: "VN-002"}}
	assertHas(t, "legacy 依赖歧义", s4.Validate(), model.CodeDepAmbiguous)

	// 唯一命中的 legacy 依赖合法。
	s5 := validTaskSnapshot(t)
	s5.Instances = append(s5.Instances, retiredInstance(t, "ti_vn004a", "VN-004", "2026-10-20T00:00:00Z", "2026-10-21T00:00:00Z"))
	s5.Instances = append(s5.Instances, func() model.TaskInstance {
		i := validTaskSnapshot(t).Instances[0]
		i.InstanceID = "ti_vn005b"
		i.DisplayID = "VN-005"
		i.Legacy = model.LegacyAnchor{ProjectID: testProject, DisplayID: "VN-005", PrimaryKey: model.LegacyPrimaryKey(testProject, "VN-005")}
		i.Versions[0].Deps = []model.Dep{{Kind: model.DepLegacy, Target: "VN-004"}}
		return i
	}())
	assertHas(t, "legacy 依赖退役目标", s5.Validate(), model.CodeDepRetired)
}

func TestContractIdentifierIsEnforced(t *testing.T) {
	s := validTaskSnapshot(t)
	s.Contract = "maw.task.instance/v2"
	assertHas(t, "错契约版本", s.Validate(), model.CodeTaskContractUnsupported)

	s2 := validTaskSnapshot(t)
	s2.ProjectID = "not-a-project"
	assertHas(t, "工程 ID 形态", s2.Validate(), model.CodeTaskFieldInvalid)
}
