package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// TestEnsureManualCompatRunReusesRunVersion 覆盖病灶 1：active_version 已升到 2，
// 复用仍占用的 Run 时必须返回该 Run 冻结的 version_no（1），不能塞回 active=2（E330）。
func TestEnsureManualCompatRunReusesRunVersion(t *testing.T) {
	f := newFixture(t)
	const id = "LF-V1"
	task := f.seedTask(id, "ready", "", []string{"mod_v1/**"})

	t.Setenv("MAW_AGENT", "cursor-lf-v1")
	if _, err := Next(f.repo, "cursor-lf-v1", false, ""); err != nil {
		t.Fatalf("next: %v", err)
	}
	task = f.task(id)

	var runVersion model.VersionNo
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		run, e := db.ActiveRunForInstance(tx, task.InstanceID)
		if e != nil {
			return e
		}
		if run == nil {
			t.Fatal("领取后应有占用 Run")
		}
		runVersion = run.VersionNo
		// 模拟 plan apply 中途升版：直接 FreezeVersion，绕过占用门禁（测 ensure 路径本身）。
		_, e = db.FreezeVersion(tx, task.InstanceID, db.InstanceDraft{
			Title: "bumped title",
			Deps:  nil,
			Spec:  map[string]any{"title": "bumped title"},
		})
		return e
	})
	if err != nil {
		t.Fatalf("升版: %v", err)
	}

	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		inst, e := db.GetInstance(tx, task.InstanceID)
		if e != nil {
			return e
		}
		if inst.ActiveVersion == nil || *inst.ActiveVersion != runVersion+1 {
			t.Fatalf("active_version 应前进到 %d，实际 %v", runVersion+1, inst.ActiveVersion)
		}
		run, ver, e := ensureManualCompatRun(tx, f.pc, task, "cursor-lf-v1", task.Worktree)
		if e != nil {
			return e
		}
		if ver != runVersion {
			t.Fatalf("复用 Run 应返回 version_no=%d，得到 %d（active=%d 会触发 E330）",
				runVersion, ver, *inst.ActiveVersion)
		}
		if run == nil || run.VersionNo != runVersion {
			t.Fatalf("复用的 Run.VersionNo=%v want %d", run, runVersion)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ensureManualCompatRun: %v", err)
	}
}

// TestFreezeDefinitionBlockedWhileOccupied 覆盖病灶 1 源头：有 Occupies Run 时拒升版。
func TestFreezeDefinitionBlockedWhileOccupied(t *testing.T) {
	f := newFixture(t)
	const id = "LF-V2"
	f.seedTask(id, "ready", "", []string{"mod_v2/**"})

	t.Setenv("MAW_AGENT", "cursor-lf-v2")
	if _, err := Next(f.repo, "cursor-lf-v2", false, ""); err != nil {
		t.Fatalf("next: %v", err)
	}
	task := f.task(id)
	task.Title = "changed while occupied"

	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return freezeIfDefinitionChanged(tx, task)
	})
	if err == nil {
		t.Fatal("占用中应拒绝升定义版本")
	}

	var active model.VersionNo
	_ = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		inst, e := db.GetInstance(tx, task.InstanceID)
		if e != nil || inst == nil || inst.ActiveVersion == nil {
			t.Fatalf("读 instance: %v", e)
		}
		active = *inst.ActiveVersion
		return nil
	})
	if active != 1 {
		t.Fatalf("拒绝后 active_version 应仍为 1，得到 %d", active)
	}
}

// TestTaskCancelReleasesLeases 覆盖病灶 2：cancel 后必须释放 lease，否则下一题 claim 同 worktree 会 E240。
func TestTaskCancelReleasesLeases(t *testing.T) {
	f := newFixture(t)
	const id = "LF-C1"
	f.seedTask(id, "ready", "", []string{"mod_c1/**"})

	t.Setenv("MAW_AGENT", "cursor-lf-c1")
	res, err := Next(f.repo, "cursor-lf-c1", false, "")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	task := f.task(id)
	wt, _ := res["worktree"].(string)
	if wt == "" {
		wt = task.Worktree
	}

	if _, err := TaskCancel(f.repo, id, CancelOptions{Reason: "test_cancel"}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got := f.task(id)
	if got.Status != "cancelled" {
		t.Fatalf("status=%s want cancelled", got.Status)
	}

	var leases int
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM resource_leases
WHERE instance_id = ? AND released_at IS NULL`, task.InstanceID).Scan(&leases)
	})
	if err != nil {
		t.Fatalf("读租约: %v", err)
	}
	if leases != 0 {
		t.Fatalf("cancel 后仍有 %d 条未释放租约", leases)
	}

	var occupying int
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		run, e := db.ActiveRunForInstance(tx, task.InstanceID)
		if e != nil {
			return e
		}
		if run != nil && run.Occupies() {
			occupying = 1
		}
		return nil
	})
	if err != nil {
		t.Fatalf("读 Run: %v", err)
	}
	if occupying != 0 {
		t.Fatal("cancel 后不应再有占用中的 Run")
	}

	// 同 Agent 再领下一题：同 worktree 路径不应再撞 E240。
	f.seedTask("LF-C2", "ready", "", []string{"mod_c2/**"})
	t.Setenv("MAW_AGENT", "cursor-lf-c1")
	if _, err := Next(f.repo, "cursor-lf-c1", false, ""); err != nil {
		t.Fatalf("cancel 后再 claim 同 Agent worktree 失败: %v (wt=%s)", err, filepath.Clean(wt))
	}
}

// TestTaskCancelIdempotentCleansLeftover 终态再 cancel：有残留则清理，干净则仍报 already terminal。
func TestTaskCancelIdempotentCleansLeftover(t *testing.T) {
	f := newFixture(t)
	const id = "LF-C3"
	f.seedTask(id, "ready", "", []string{"mod_c3/**"})
	t.Setenv("MAW_AGENT", "cursor-lf-c3")
	if _, err := Next(f.repo, "cursor-lf-c3", false, ""); err != nil {
		t.Fatalf("next: %v", err)
	}
	if _, err := TaskCancel(f.repo, id, CancelOptions{Reason: "first"}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// 干净终态再调：应报 already terminal。
	if _, err := TaskCancel(f.repo, id, CancelOptions{Reason: "again"}); err == nil {
		t.Fatal("干净终态再 cancel 应失败")
	}
}

// TestReconcileReportsAndFixesTerminalLeak 旧路径残留：手动只改 status 后 reconcile --apply 应放租约。
func TestReconcileReportsAndFixesTerminalLeak(t *testing.T) {
	f := newFixture(t)
	const id = "LF-R1"
	f.seedTask(id, "ready", "", []string{"mod_r1/**"})
	t.Setenv("MAW_AGENT", "cursor-lf-r1")
	if _, err := Next(f.repo, "cursor-lf-r1", false, ""); err != nil {
		t.Fatalf("next: %v", err)
	}
	task := f.task(id)
	// 模拟旧 cancel：只改 tasks.status，不放租约。
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		trow, e := db.GetTask(tx, f.pc.ID(), id)
		if e != nil || trow == nil {
			return e
		}
		trow.Status = "cancelled"
		trow.Owner = ""
		return db.UpsertTask(tx, trow)
	})
	if err != nil {
		t.Fatalf("模拟旧 cancel: %v", err)
	}

	preview, err := Reconcile(f.repo, ReconcileOptions{})
	if err != nil {
		t.Fatalf("reconcile preview: %v", err)
	}
	findings, _ := preview["findings"].([]reconcileFinding)
	if findings == nil {
		// JSON 形状：interface 切片
		raw, _ := preview["findings"].([]any)
		if len(raw) == 0 {
			t.Fatalf("应报告 terminal_leak，得到 %#v", preview)
		}
	} else if len(findings) == 0 {
		t.Fatalf("应报告 terminal_leak，得到 %#v", preview)
	}

	// apply 需要 lead；测试里用 orchestrator（system actor）。
	t.Setenv("MAW_AGENT", "orchestrator")
	applied, err := Reconcile(f.repo, ReconcileOptions{Apply: true})
	if err != nil {
		t.Fatalf("reconcile apply: %v", err)
	}
	if fixed, _ := applied["fixed"].(int); fixed < 1 {
		// Result 里 int 可能是别的数值类型
		if n, ok := asInt(applied["fixed"]); !ok || n < 1 {
			t.Fatalf("应至少修复 1 项: %#v", applied)
		}
	}

	var leases int
	_ = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM resource_leases
WHERE instance_id = ? AND released_at IS NULL`, task.InstanceID).Scan(&leases)
	})
	if leases != 0 {
		t.Fatalf("apply 后仍有 %d 条未释放租约", leases)
	}
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// TestPlanApplyRetireGuard 片段蓝图一次退役 ≥2 道 idle 必须显式 AllowRetire。
func TestPlanApplyRetireGuard(t *testing.T) {
	f := newFixture(t)
	f.seedTask("RG-1", "ready", "", []string{"mod_rg1/**"})
	f.seedTask("RG-2", "ready", "", []string{"mod_rg2/**"})
	f.seedTask("RG-3", "ready", "", []string{"mod_rg3/**"})

	bp := filepath.Join(f.repo, "fragment.toml")
	if err := os.WriteFile(bp, []byte(`
[meta]
name = "frag"
version = 1

[[task]]
id = "RG-1"
title = "keep one"
priority = 10
write_scope = ["mod_rg1/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 10
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := PlanApplyWith(bp, f.repo, PlanApplyOptions{Yes: true}); err == nil {
		t.Fatal("片段 apply 退役 2 道以上应被拒")
	}
	if _, err := PlanApplyWith(bp, f.repo, PlanApplyOptions{Yes: true, AllowRetire: true}); err != nil {
		t.Fatalf("AllowRetire 后应成功: %v", err)
	}
	if f.task("RG-1").Status == "" {
		t.Fatal("RG-1 应保留")
	}
}
