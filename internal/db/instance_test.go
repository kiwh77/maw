package db

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
)

const testProject = "716c30878ba1b95f"

// seedLegacyDB 造一个 v4 之前的库：只有基线表结构、没有台账、tasks 没有 instance_id。
func seedLegacyDB(t *testing.T, path string) {
	t.Helper()
	if err := ensureParentDir(path); err != nil {
		t.Fatal(err)
	}
	conn, err := openConn(path, writeConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES ('schema_version', '3')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO tasks(project_id, id, title, status, priority, merge_order,
  deps_json, write_scope_json, read_scope_json, acceptance_json, revision, extra_json)
VALUES (?, 'VN-001', '旧题', 'ready', 10, 10, '["VN-000"]', '["internal/db/**"]', '[]', '["ok"]', 0, '{}')`,
		testProject); err != nil {
		t.Fatal(err)
	}
}

func hasCode(err error, code model.Code) bool {
	var me *model.Error
	return errors.As(err, &me) && me.Code == code
}

func newTask(display string) *Task {
	return &Task{
		ProjectID: testProject, ID: display, Title: "题 " + display, Status: "ready",
		Priority: 10, MergeOrder: 10,
		Deps: []string{}, WriteScope: []string{"internal/db/**"}, ReadScope: []string{},
		Acceptance: []string{"ok"}, Extra: map[string]any{},
	}
}

var instanceIDRe = regexp.MustCompile(`^ti_[a-z0-9]{6,}$`)

// TestIdentityMigrationBackfillsLegacyTasks 锁定存量库的补账：每行拿到唯一实例与 v1 快照，
// 重放迁移不改身份。
func TestIdentityMigrationBackfillsLegacyTasks(t *testing.T) {
	path := freshPath(t)
	seedLegacyDB(t, path)
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}

	var instanceID string
	var taskCount, instanceCount, versionCount int
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT instance_id FROM tasks WHERE project_id = ? AND id = 'VN-001'`,
			testProject).Scan(&instanceID); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&taskCount); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT COUNT(*) FROM task_instances`).Scan(&instanceCount); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT COUNT(*) FROM task_versions`).Scan(&versionCount)
	}); err != nil {
		t.Fatal(err)
	}
	if !instanceIDRe.MatchString(instanceID) {
		t.Fatalf("补账后的 instance_id 形态非法: %q", instanceID)
	}
	if taskCount != 1 || instanceCount != 1 || versionCount != 1 {
		t.Fatalf("tasks=%d instances=%d versions=%d", taskCount, instanceCount, versionCount)
	}

	var inst *model.TaskInstance
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		var err error
		inst, err = ActiveInstance(tx, testProject, "VN-001")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if inst == nil || inst.ActiveVersion == nil || *inst.ActiveVersion != 1 {
		t.Fatalf("活跃实例与 v1 指针不对: %+v", inst)
	}
	if len(inst.Versions) != 1 || len(inst.Versions[0].Deps) != 1 ||
		inst.Versions[0].Deps[0].Kind != model.DepLegacy || inst.Versions[0].Deps[0].Target != "VN-000" {
		t.Fatalf("存量 deps_json 应按 legacy 冻结: %+v", inst.Versions)
	}
	if inst.Legacy.PrimaryKey != model.LegacyPrimaryKey(model.ProjectID(testProject), "VN-001") {
		t.Fatalf("legacy 主键错位: %+v", inst.Legacy)
	}

	// 重放迁移：身份与版本都不该变。
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	var again string
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT instance_id FROM tasks WHERE id = 'VN-001'`).Scan(&again)
	}); err != nil {
		t.Fatal(err)
	}
	if again != instanceID {
		t.Fatalf("重放迁移换了身份: %q → %q", instanceID, again)
	}
}

func TestUpsertTaskBindsIdentityAndRebuildStartsNewHistory(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	var firstID string
	if err := WithTx(path, func(tx *sql.Tx) error {
		task := newTask("VN-100")
		if err := UpsertTask(tx, task); err != nil {
			return err
		}
		firstID = task.InstanceID
		if !instanceIDRe.MatchString(firstID) {
			t.Fatalf("写入后应回填实例身份: %q", firstID)
		}
		// 同编号再写一次：复用同一个实例，不新开版本。
		task.Status = "done"
		if err := UpsertTask(tx, task); err != nil {
			return err
		}
		if task.InstanceID != firstID {
			t.Fatalf("同编号被换了身份: %q → %q", firstID, task.InstanceID)
		}
		inst, err := GetInstance(tx, firstID)
		if err != nil {
			return err
		}
		if len(inst.Versions) != 1 {
			t.Fatalf("普通状态更新不应冻结新版本，得到 %d 版", len(inst.Versions))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 删除即退役：别名释放，历史保留。
	if err := WithTx(path, func(tx *sql.Tx) error {
		return DeleteTask(tx, testProject, "VN-100")
	}); err != nil {
		t.Fatal(err)
	}
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		inst, err := GetInstance(tx, firstID)
		if err != nil {
			return err
		}
		if inst.State != model.InstanceStateRetired || inst.ActiveVersion != nil {
			t.Fatalf("删除后实例应退役且不留活跃指针: %+v", inst)
		}
		active, err := ActiveInstance(tx, testProject, "VN-100")
		if err != nil {
			return err
		}
		if active != nil {
			t.Fatalf("别名应已释放，仍有活跃实例 %s", active.InstanceID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 同编号重建：新身份、新 v1，旧历史按 instance_id 分开。
	var rebuiltID string
	if err := WithTx(path, func(tx *sql.Tx) error {
		task := newTask("VN-100")
		if err := UpsertTask(tx, task); err != nil {
			return err
		}
		rebuiltID = task.InstanceID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if rebuiltID == firstID {
		t.Fatal("重建同一编号必须换 instance_id")
	}
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		history, err := DisplayHistory(tx, testProject, "VN-100")
		if err != nil {
			return err
		}
		if len(history) != 2 {
			t.Fatalf("编号历史应有 2 条，得到 %d", len(history))
		}
		old, err := GetInstance(tx, firstID)
		if err != nil {
			return err
		}
		if len(old.Versions) != 1 || old.Versions[0].Title != "题 VN-100" {
			t.Fatalf("旧实例历史被改写: %+v", old.Versions)
		}
		snap, err := TaskSnapshot(tx, testProject)
		if err != nil {
			return err
		}
		if v := snap.Validate(); len(v) != 0 {
			t.Fatalf("快照不满足契约: %v", v)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFreezeVersionIsAppendOnlyMonotonicAndImmutable(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	var instanceID string
	if err := WithTx(path, func(tx *sql.Tx) error {
		inst, err := CreateInstance(tx, testProject, "VN-200", InstanceDraft{Title: "v1"})
		if err != nil {
			return err
		}
		instanceID = string(inst.InstanceID)
		for _, title := range []string{"v2", "v3"} {
			if _, err := FreezeVersion(tx, instanceID, InstanceDraft{Title: title}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := WithReadTx(path, func(tx *sql.Tx) error {
		inst, err := GetInstance(tx, instanceID)
		if err != nil {
			return err
		}
		if len(inst.Versions) != 3 {
			t.Fatalf("应有 3 版，得到 %d", len(inst.Versions))
		}
		for i, v := range inst.Versions {
			if int(v.VersionNo) != i+1 {
				t.Fatalf("版本号跳号: %+v", inst.Versions)
			}
			if i > 0 && !v.FrozenAt.After(inst.Versions[i-1].FrozenAt) {
				t.Fatalf("冻结时间未严格递增: %+v", inst.Versions)
			}
		}
		if inst.ActiveVersion == nil || *inst.ActiveVersion != 3 {
			t.Fatalf("活跃指针应停在最大版本: %+v", inst.ActiveVersion)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 已冻结版本连库层直写都不允许。
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE task_versions SET title = 'rewritten' WHERE instance_id = ?`, instanceID)
		return err
	}); err == nil || !strings.Contains(err.Error(), "不可修改") {
		t.Fatalf("UPDATE 已冻结版本应被拒绝，得到 %v", err)
	}
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM task_versions WHERE instance_id = ?`, instanceID)
		return err
	}); err == nil || !strings.Contains(err.Error(), "不可删除") {
		t.Fatalf("DELETE 已冻结版本应被拒绝，得到 %v", err)
	}

	// 退役之后再冻结 → E142。
	if err := WithTx(path, func(tx *sql.Tx) error {
		if err := RetireInstance(tx, instanceID, RetireRemovedFromPlan); err != nil {
			return err
		}
		_, err := FreezeVersion(tx, instanceID, InstanceDraft{Title: "late"})
		return err
	}); !hasCode(err, model.CodeFrozenAfterRetire) {
		t.Fatalf("退役后冻结应报 E142，得到 %v", err)
	}
}

func TestRetireIsAtomicAndReleasesAlias(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	var instanceID string
	if err := WithTx(path, func(tx *sql.Tx) error {
		task := newTask("VN-300")
		if err := UpsertTask(tx, task); err != nil {
			return err
		}
		instanceID = task.InstanceID
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 退役失败（缺原因）必须整体回滚：状态与 tasks 行都不动。
	if err := WithTx(path, func(tx *sql.Tx) error {
		return RetireInstance(tx, instanceID, "  ")
	}); !hasCode(err, model.CodeRetiredIncomplete) {
		t.Fatalf("缺原因应报 E140，得到 %v", err)
	}
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		inst, err := GetInstance(tx, instanceID)
		if err != nil {
			return err
		}
		if inst.State != model.InstanceStateActive {
			t.Fatalf("失败的退役改动了状态: %+v", inst)
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE instance_id = ?`, instanceID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("失败的退役删掉了任务行: %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 归档与别名释放同事务：退役后 tasks 行消失，编号可被新实例接管。
	if err := WithTx(path, func(tx *sql.Tx) error {
		return RetireInstance(tx, instanceID, RetireReset)
	}); err != nil {
		t.Fatal(err)
	}
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		inst, err := GetInstance(tx, instanceID)
		if err != nil {
			return err
		}
		if inst.State != model.InstanceStateRetired || inst.RetiredAt == nil ||
			inst.RetiredReason == nil || *inst.RetiredReason != RetireReset || inst.ActiveVersion != nil {
			t.Fatalf("退役归档不完整: %+v", inst)
		}
		if len(inst.Versions) != 1 {
			t.Fatalf("退役不该丢版本快照: %+v", inst.Versions)
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE instance_id = ?`, instanceID).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("退役后 tasks 行应已释放，仍有 %d 行", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := WithTx(path, func(tx *sql.Tx) error {
		inst, err := CreateInstance(tx, testProject, "VN-300", InstanceDraft{Title: "重建"})
		if err != nil {
			return err
		}
		if string(inst.InstanceID) == instanceID {
			t.Fatal("新实例复用了旧身份")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAmbiguousLegacyAnchorNeverActive(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	if err := WithTx(path, func(tx *sql.Tx) error {
		_, err := RecordAmbiguousAnchor(tx, testProject, "VN-400", "", InstanceDraft{Title: "x"})
		return err
	}); err == nil {
		t.Fatal("歧义锚点必须写说明")
	}

	var anchorID string
	if err := WithTx(path, func(tx *sql.Tx) error {
		inst, err := RecordAmbiguousAnchor(tx, testProject, "VN-400",
			"同一编号有两条互斥历史，无证据判定归属", InstanceDraft{Title: "疑似旧定义"})
		if err != nil {
			return err
		}
		anchorID = string(inst.InstanceID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := WithReadTx(path, func(tx *sql.Tx) error {
		anchor, err := GetInstance(tx, anchorID)
		if err != nil {
			return err
		}
		if anchor.State != model.InstanceStateRetired || !anchor.Legacy.Ambiguous ||
			anchor.Legacy.AmbiguityNote == nil || *anchor.Legacy.AmbiguityNote == "" {
			t.Fatalf("歧义锚点状态不对: %+v", anchor)
		}
		active, err := ActiveInstance(tx, testProject, "VN-400")
		if err != nil {
			return err
		}
		if active != nil {
			t.Fatalf("歧义历史不能充当活跃别名: %s", active.InstanceID)
		}
		snap, err := TaskSnapshot(tx, testProject)
		if err != nil {
			return err
		}
		if v := snap.Validate(); len(v) != 0 {
			t.Fatalf("歧义锚点违反契约: %v", v)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDependencyFollowsContract(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	var aID, bID string
	if err := WithTx(path, func(tx *sql.Tx) error {
		a, err := CreateInstance(tx, testProject, "VN-410", InstanceDraft{Title: "A"})
		if err != nil {
			return err
		}
		aID = string(a.InstanceID)
		b, err := CreateInstance(tx, testProject, "VN-411", InstanceDraft{Title: "B"})
		if err != nil {
			return err
		}
		bID = string(b.InstanceID)
		return RetireInstance(tx, bID, RetireRemovedFromPlan)
	}); err != nil {
		t.Fatal(err)
	}

	// 唯一命中的 legacy 编号可解析（存量 deps_json 形态）。
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		got, err := ResolveDependency(tx, testProject, bID, model.Dep{Kind: model.DepLegacy, Target: "VN-410"})
		if err != nil {
			return err
		}
		if got != aID {
			t.Fatalf("legacy 应解析到 %s，得到 %s", aID, got)
		}
		// instance 形式精确命中。
		if _, err := ResolveDependency(tx, testProject, bID, model.Dep{Kind: model.DepInstance, Target: aID}); err != nil {
			t.Fatalf("instance 依赖应可解析: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		self string
		dep  model.Dep
		code model.Code
	}{
		{"不存在", aID, model.Dep{Kind: model.DepInstance, Target: "ti_nosuchid"}, model.CodeDepMissing},
		{"自身", aID, model.Dep{Kind: model.DepInstance, Target: aID}, model.CodeDepSelf},
		{"已退役", aID, model.Dep{Kind: model.DepInstance, Target: bID}, model.CodeDepRetired},
		{"编号不存在", aID, model.Dep{Kind: model.DepLegacy, Target: "VN-999"}, model.CodeDepMissing},
		{"编号命中退役", aID, model.Dep{Kind: model.DepLegacy, Target: "VN-411"}, model.CodeDepRetired},
	}
	for _, c := range cases {
		err := WithReadTx(path, func(tx *sql.Tx) error {
			_, err := ResolveDependency(tx, testProject, c.self, c.dep)
			return err
		})
		if !hasCode(err, c.code) {
			t.Fatalf("%s: 期望 %s，得到 %v", c.name, c.code, err)
		}
	}

	// 编号重建后同一 legacy 依赖命中两个实例 → E153，必须改用 instance 形式。
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, err := CreateInstance(tx, testProject, "VN-411", InstanceDraft{Title: "B2"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		_, err := ResolveDependency(tx, testProject, aID, model.Dep{Kind: model.DepLegacy, Target: "VN-411"})
		return err
	}); !hasCode(err, model.CodeDepAmbiguous) {
		t.Fatalf("歧义编号应报 E153，得到 %v", err)
	}
}

func TestBindTaskIdentityRejectsStaleReference(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	var retiredID string
	if err := WithTx(path, func(tx *sql.Tx) error {
		inst, err := CreateInstance(tx, testProject, "VN-420", InstanceDraft{Title: "旧"})
		if err != nil {
			return err
		}
		retiredID = string(inst.InstanceID)
		return RetireInstance(tx, retiredID, RetireRemovedFromPlan)
	}); err != nil {
		t.Fatal(err)
	}

	task := newTask("VN-420")
	task.InstanceID = retiredID
	if err := WithTx(path, func(tx *sql.Tx) error {
		return UpsertTask(tx, task)
	}); !hasCode(err, model.CodeDepRetired) {
		t.Fatalf("指向退役实例的引用应被拒绝，得到 %v", err)
	}

	// 编号对不上同样拒绝：否则编号重建就会串到别人的历史上。
	task = newTask("VN-421")
	task.InstanceID = retiredID
	if err := WithTx(path, func(tx *sql.Tx) error {
		return UpsertTask(tx, task)
	}); !hasCode(err, model.CodeLegacyAnchorMissing) {
		t.Fatalf("跨编号引用应被拒绝，得到 %v", err)
	}
}
