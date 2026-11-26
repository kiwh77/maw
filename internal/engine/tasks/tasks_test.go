package tasks

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

const testProject = model.ProjectID("716c30878ba1b95f")

var (
	t0 = time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	t1 = t0.Add(24 * time.Hour)
)

type seqIDs struct{ n int }

func (s *seqIDs) New() (model.InstanceID, error) {
	s.n++
	return model.InstanceID(fmt.Sprintf("ti_seq%03d", s.n)), nil
}

func testInstance(id, display, title string, frozen time.Time, deps ...model.Dep) model.TaskInstance {
	v := model.VersionNo(1)
	return model.TaskInstance{
		InstanceID:    model.InstanceID(id),
		DisplayID:     model.DisplayID(display),
		State:         model.InstanceStateActive,
		CreatedAt:     t0,
		ActiveVersion: &v,
		Versions: []model.TaskVersion{{
			VersionNo: 1, FrozenAt: frozen, Title: title, Deps: deps,
		}},
		Legacy: model.LegacyAnchor{
			ProjectID: testProject, DisplayID: model.DisplayID(display),
			PrimaryKey: model.LegacyPrimaryKey(testProject, model.DisplayID(display)),
		},
	}
}

func retiredInstance(id, display, title string, frozen, retiredAt time.Time) model.TaskInstance {
	inst := testInstance(id, display, title, frozen)
	reason := "removed_from_plan"
	inst.State = model.InstanceStateRetired
	inst.ActiveVersion = nil
	inst.RetiredAt = &retiredAt
	inst.RetiredReason = &reason
	return inst
}

func testSnapshot(instances ...model.TaskInstance) *model.TaskSnapshot {
	return &model.TaskSnapshot{
		Contract: model.TaskContract, ProjectID: testProject, GeneratedAt: t0, Instances: instances,
	}
}

func assertCode(t *testing.T, err error, want model.Code) {
	t.Helper()
	var me *model.Error
	if !errors.As(err, &me) {
		t.Fatalf("期望契约错误 %s，得到 %v", want, err)
	}
	if me.Code != want {
		t.Fatalf("错误码 = %s，期望 %s：%s", me.Code, want, me.Msg)
	}
}

func TestIncrementalKeepsInstancesAndFreezesNewVersion(t *testing.T) {
	snap := testSnapshot(
		testInstance("ti_old101", "VN-001", "A", t0),
		testInstance("ti_old102", "VN-002", "B", t0,
			model.Dep{Kind: model.DepInstance, Target: "ti_old101"},
			model.Dep{Kind: model.DepInstance, Target: "ti_old103"}),
		testInstance("ti_old104", "VN-004", "D", t0),
	)
	// 让 VN-002 的依赖目标真实存在且不是自身，避免快照校验失败。
	snap.Instances = append(snap.Instances, testInstance("ti_old103", "VN-003", "C", t0))

	svc := New(&seqIDs{})
	plan, err := svc.Plan(PlanRequest{
		ProjectID: testProject,
		Snapshot:  snap,
		Mode:      ModeIncremental,
		Desired: []Desired{
			{DisplayID: "VN-001", Title: "A2"},
			{DisplayID: "VN-002", Title: "B", Deps: []model.Dep{
				{Kind: model.DepInstance, Target: "ti_old103"},
				{Kind: model.DepInstance, Target: "ti_old101"},
			}},
			{DisplayID: "VN-003", Title: "C"},
			{DisplayID: "VN-005", Title: "E"},
		},
		Now: t1,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Mode != ModeIncremental {
		t.Fatalf("默认应为增量: %+v", plan.Mode)
	}
	if len(plan.Freezes) != 1 || plan.Freezes[0].InstanceID != "ti_old101" || plan.Freezes[0].Version != 2 {
		t.Fatalf("VN-001 应追加 v2: %+v", plan.Freezes)
	}
	if len(plan.Unchanged) != 2 {
		t.Fatalf("依赖顺序变化不应产生新版本: %+v", plan.Unchanged)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].DisplayID != "VN-005" {
		t.Fatalf("新题应创建新实例: %+v", plan.Creates)
	}
	if len(plan.Retires) != 1 || plan.Retires[0].DisplayID != "VN-004" {
		t.Fatalf("蓝图外的活跃实例应归档: %+v", plan.Retires)
	}
	for _, r := range plan.Retires {
		if r.Reason != DefaultRetireReason {
			t.Fatalf("默认归档原因 = %q", r.Reason)
		}
	}

	// 输入快照不被改写：v1 仍是唯一版本且内容未变。
	orig := snap.Task("ti_old101")
	if orig.Versions[0].Title != "A" || len(orig.Versions) != 1 {
		t.Fatalf("输入快照被改写: %+v", orig)
	}

	inst := plan.Snapshot.Task("ti_old101")
	if inst.Versions[0].Title != "A" || !inst.Versions[0].FrozenAt.Equal(t0) {
		t.Fatalf("旧版本必须保持不变: %+v", inst.Versions[0])
	}
	if inst.ActiveVersionOf() == nil || inst.ActiveVersionOf().VersionNo != 2 || inst.ActiveVersionOf().Title != "A2" {
		t.Fatalf("活跃版本应前进到 v2: %+v", inst.ActiveVersion)
	}

	retired := plan.Snapshot.Task("ti_old104")
	if retired.State != model.InstanceStateRetired || retired.ActiveVersion != nil || len(retired.Versions) != 1 {
		t.Fatalf("归档应保留版本快照: %+v", retired)
	}
	if errs := plan.Snapshot.Validate(); len(errs) > 0 {
		t.Fatalf("结果快照应满足契约: %s", errs[0].Msg)
	}
}

func TestResetRetiresOldInstancesAndAllocatesNewIDs(t *testing.T) {
	snap := testSnapshot(
		testInstance("ti_aaa111", "VN-001", "A", t0),
		testInstance("ti_bbb111", "VN-002", "B", t0),
	)
	svc := New(&seqIDs{})
	plan, err := svc.Plan(PlanRequest{
		ProjectID: testProject,
		Snapshot:  snap,
		Mode:      ModeReset,
		Desired: []Desired{
			{DisplayID: "VN-001", Title: "A"},
		},
		ActiveRuns: []ActiveRun{{RunID: "run_100", Instance: "ti_aaa111", Version: 1}},
		Now:        t1,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Retires) != 2 {
		t.Fatalf("reset 应退役全部旧实例: %+v", plan.Retires)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].InstanceID == "ti_aaa111" {
		t.Fatalf("reset 必须分配新实例号: %+v", plan.Creates)
	}
	if len(plan.Pinned) != 1 || plan.Pinned[0].Version != 1 ||
		plan.Pinned[0].Instance != "ti_aaa111" {
		t.Fatalf("活跃 Run 应固定在旧版本: %+v", plan.Pinned)
	}
	if plan.Pinned[0].Note == "" || len(plan.Notes) == 0 {
		t.Fatalf("必须解释占用未清空: %+v / %+v", plan.Pinned, plan.Notes)
	}

	old := plan.Snapshot.Task("ti_aaa111")
	if old == nil || old.State != model.InstanceStateRetired || len(old.Versions) != 1 {
		t.Fatalf("旧实例应保留为退役快照: %+v", old)
	}
	active := plan.Snapshot.Task(plan.Creates[0].InstanceID)
	if active == nil || active.State != model.InstanceStateActive || active.DisplayID != "VN-001" {
		t.Fatalf("新实例应占用显示编号: %+v", active)
	}
	if errs := plan.Snapshot.Validate(); len(errs) > 0 {
		t.Fatalf("reset 结果应满足契约: %s", errs[0].Msg)
	}
}

func TestPinnedRunKeepsFrozenVersionAcrossUpdate(t *testing.T) {
	snap := testSnapshot(testInstance("ti_aaa111", "VN-001", "A", t0))
	svc := New(&seqIDs{})
	plan, err := svc.Plan(PlanRequest{
		ProjectID:  testProject,
		Snapshot:   snap,
		Desired:    []Desired{{DisplayID: "VN-001", Title: "A2"}},
		ActiveRuns: []ActiveRun{{RunID: "run_100", Instance: "ti_aaa111", Version: 1}},
		Now:        t1,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	inst := plan.Snapshot.Task("ti_aaa111")
	if len(inst.Versions) != 2 || inst.Versions[0].Title != "A" || inst.Versions[1].Title != "A2" {
		t.Fatalf("新版本应追加而不是改写: %+v", inst.Versions)
	}
	if len(plan.Pinned) != 1 || plan.Pinned[0].Version != 1 {
		t.Fatalf("活跃 Run 必须继续固定 v1: %+v", plan.Pinned)
	}
	if !inst.Versions[0].FrozenAt.Before(inst.Versions[1].FrozenAt) {
		t.Fatalf("冻结时间必须严格递增: %+v", inst.Versions)
	}
}

func TestLegacyDependencyAmbiguityIsRejected(t *testing.T) {
	snap := testSnapshot(
		testInstance("ti_new001", "VN-005", "N", t0),
		retiredInstance("ti_old100", "VN-005", "O", t0, t0),
	)
	svc := New(&seqIDs{})
	_, err := svc.Plan(PlanRequest{
		ProjectID: testProject,
		Snapshot:  snap,
		Desired: []Desired{{
			DisplayID: "VN-006", Title: "X",
			Deps: []model.Dep{{Kind: model.DepLegacy, Target: "VN-005"}},
		}},
		Now: t1,
	})
	assertCode(t, err, model.CodeDepAmbiguous)
}

func TestLegacyDependencyResolvesToStableInstance(t *testing.T) {
	snap := testSnapshot(testInstance("ti_bbb111", "VN-002", "B", t0))
	svc := New(&seqIDs{})
	plan, err := svc.Plan(PlanRequest{
		ProjectID: testProject,
		Snapshot:  snap,
		Desired: []Desired{
			{DisplayID: "VN-002", Title: "B"},
			{DisplayID: "VN-003", Title: "C",
				Deps: []model.Dep{{Kind: model.DepLegacy, Target: "VN-002"}}},
		},
		Now: t1,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	created := plan.Snapshot.Task(plan.Creates[0].InstanceID)
	dep := created.Versions[0].Deps[0]
	if dep.Kind != model.DepInstance || dep.Target != "ti_bbb111" {
		t.Fatalf("新写入依赖必须转为 instance: %+v", dep)
	}
	if errs := plan.Snapshot.Validate(); len(errs) > 0 {
		t.Fatalf("快照应满足契约: %s", errs[0].Msg)
	}
}

func TestDependencyOnRetiredAndSelfAreRejected(t *testing.T) {
	retiredOnly := testSnapshot(retiredInstance("ti_old100", "VN-002", "B", t0, t0))
	svc := New(&seqIDs{})
	_, err := svc.Plan(PlanRequest{
		ProjectID: testProject,
		Snapshot:  retiredOnly,
		Desired: []Desired{
			{DisplayID: "VN-003", Title: "C",
				Deps: []model.Dep{{Kind: model.DepLegacy, Target: "VN-002"}}},
		},
		Now: t1,
	})
	assertCode(t, err, model.CodeDepRetired)

	_, err = svc.Plan(PlanRequest{
		ProjectID: testProject,
		Desired: []Desired{{
			DisplayID: "VN-003", Title: "C",
			Deps: []model.Dep{{Kind: model.DepLegacy, Target: "VN-003"}},
		}},
		Now: t1,
	})
	assertCode(t, err, model.CodeDepSelf)
}

func TestArchivePreservesSnapshotAndRejectsRepeat(t *testing.T) {
	snap := testSnapshot(testInstance("ti_aaa111", "VN-001", "A", t0))
	svc := New(&seqIDs{})
	plan, err := svc.Archive(ArchiveRequest{
		ProjectID: testProject, Snapshot: snap, Instance: "ti_aaa111",
		Reason: "removed_from_plan", Now: t1,
	})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	inst := plan.Snapshot.Task("ti_aaa111")
	if inst.State != model.InstanceStateRetired || inst.ActiveVersion != nil ||
		inst.RetiredAt == nil || *inst.RetiredReason != "removed_from_plan" {
		t.Fatalf("归档结果 = %+v", inst)
	}
	if len(inst.Versions) != 1 {
		t.Fatalf("归档必须保留版本快照: %+v", inst.Versions)
	}
	if snap.Task("ti_aaa111").State != model.InstanceStateActive {
		t.Fatal("输入快照被改写")
	}

	if _, err := svc.Archive(ArchiveRequest{ProjectID: testProject, Snapshot: snap,
		Instance: "ti_aaa111", Now: t1}); err == nil {
		t.Fatal("归档缺少原因必须拒绝")
	}
	if _, err := svc.Archive(ArchiveRequest{ProjectID: testProject, Snapshot: plan.Snapshot,
		Instance: "ti_aaa111", Reason: "manual", Now: t1}); err == nil {
		t.Fatal("重复归档必须拒绝")
	}
}

func TestAllocatorCollisionIsSkipped(t *testing.T) {
	snap := testSnapshot(testInstance("ti_aaa111", "VN-001", "A", t0))
	svc := New(&fixedIDs{ids: []model.InstanceID{"ti_aaa111", "ti_fresh1"}})
	plan, err := svc.Plan(PlanRequest{
		ProjectID: testProject, Snapshot: snap, Mode: ModeReset,
		Desired: []Desired{{DisplayID: "VN-001", Title: "A"}}, Now: t1,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Creates[0].InstanceID != "ti_fresh1" {
		t.Fatalf("必须跳过已占用实例号: %+v", plan.Creates)
	}
}

type fixedIDs struct {
	ids []model.InstanceID
	i   int
}

func (f *fixedIDs) New() (model.InstanceID, error) {
	if f.i >= len(f.ids) {
		return "", fmt.Errorf("分配器已耗尽")
	}
	id := f.ids[f.i]
	f.i++
	return id, nil
}
