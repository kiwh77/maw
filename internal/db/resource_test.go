package db

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
)

func hasPort(err error, code model.PortErrorCode) bool {
	var me *model.Error
	return errors.As(err, &me) && me.Port == code
}

func mustWithTx(t *testing.T, path string, fn func(*sql.Tx) error) {
	t.Helper()
	if err := WithTx(path, fn); err != nil {
		t.Fatal(err)
	}
}

func TestResourceKeyNormalization(t *testing.T) {
	a, err := Resource{Kind: model.LeaseWorkspace, Path: "/tmp/ws/a/"}.Key()
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"/tmp/ws/a", "/tmp/ws/./a", "/tmp/ws//a", "/tmp/ws/b/../a"} {
		got, err := Resource{Kind: model.LeaseWorkspace, Path: variant}.Key()
		if err != nil {
			t.Fatalf("%s: %v", variant, err)
		}
		if got != a {
			t.Fatalf("%s 归一化为 %q，期望 %q", variant, got, a)
		}
	}
	if _, err := (Resource{Kind: model.LeaseWorkspace, Path: "relative/ws"}).Key(); !hasCode(err, model.CodeRuntimeFieldInvalid) {
		t.Fatalf("相对路径应被拒绝，得到 %v", err)
	}
	if _, err := (Resource{Kind: model.LeaseNodeSlot, NodeID: "n1", Slot: 0}).Key(); !hasCode(err, model.CodeRuntimeFieldInvalid) {
		t.Fatalf("槽号必须从 1 开始，得到 %v", err)
	}
}

func TestLeaseHasSingleEffectiveHolder(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	mustWithTx(t, path, func(tx *sql.Tx) error {
		if _, err := AcquireLease(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_aaaaaa", RunID: "run_aaa", Generation: 1,
		}, Resource{Kind: model.LeaseWorkspace, Path: "/tmp/ws/one"}); err != nil {
			return err
		}
		// 同一目录换个写法仍是同一资源。
		_, err := AcquireLease(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_bbbbbb", RunID: "run_bbb", Generation: 1,
		}, Resource{Kind: model.LeaseWorkspace, Path: "/tmp/ws/one/"})
		if !hasCode(err, model.CodeDoubleOccupancy) {
			t.Fatalf("同目录第二个持有者应报 E240，得到 %v", err)
		}
		// 不同目录可以并存。
		if _, err := AcquireLease(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_bbbbbb", RunID: "run_bbb", Generation: 1,
		}, Resource{Kind: model.LeaseWorkspace, Path: "/tmp/ws/two"}); err != nil {
			return err
		}
		// 合入位按工程唯一。
		if _, err := AcquireLease(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_aaaaaa", Generation: 1,
		}, Resource{Kind: model.LeaseMerge, ProjectID: testProject}); err != nil {
			return err
		}
		_, err = AcquireLease(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_bbbbbb", Generation: 1,
		}, Resource{Kind: model.LeaseMerge, ProjectID: testProject})
		if !hasCode(err, model.CodeDoubleOccupancy) {
			t.Fatalf("同工程合入位应唯一，得到 %v", err)
		}
		return nil
	})
}

func TestReleaseNeedsGenerationAndEvidence(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	var leaseID string
	mustWithTx(t, path, func(tx *sql.Tx) error {
		if err := UpsertNode(tx, &Node{ID: "n1", SlotLimit: 1}); err != nil {
			return err
		}
		l, err := AcquireNodeSlot(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_aaaaaa", RunID: "run_aaa", Generation: 1,
		}, "n1")
		if err != nil {
			return err
		}
		leaseID = l.ID
		if l.ResourceKey != "node:n1#1" {
			t.Fatalf("资源键 = %q", l.ResourceKey)
		}
		return nil
	})

	// 进程未停稳：不得放槽（E241）。
	if err := WithTx(path, func(tx *sql.Tx) error {
		return ReleaseLease(tx, leaseID, 1, "release", ReleaseCondition{})
	}); !hasCode(err, model.CodeSlotReleasedBusy) {
		t.Fatalf("未确认停止应报 E241，得到 %v", err)
	}
	// 代际不符：不得释放（E243）。
	if err := WithTx(path, func(tx *sql.Tx) error {
		return ReleaseLease(tx, leaseID, 2, "release", ReleaseCondition{ProcessStopConfirmed: true})
	}); !hasCode(err, model.CodeReleaseGeneration) {
		t.Fatalf("代际不符应报 E243，得到 %v", err)
	}

	// 正常释放，并留下 lease.released 事件。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		return ReleaseLease(tx, leaseID, 1, "process stopped", ReleaseCondition{ProcessStopConfirmed: true})
	})
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		l, err := GetLease(tx, leaseID)
		if err != nil {
			return err
		}
		if !l.Released() || l.ReleaseGeneration != 1 {
			t.Fatalf("释放状态不对: %+v", l)
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE type = ? AND data_json LIKE ?`,
			EventLeaseReleased, "%"+leaseID+"%").Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("应有 1 条 lease.released 事件，得到 %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 重放同一次释放是幂等的。
	if err := WithTx(path, func(tx *sql.Tx) error {
		return ReleaseLease(tx, leaseID, 1, "process stopped", ReleaseCondition{ProcessStopConfirmed: true})
	}); err != nil {
		t.Fatalf("同代际重放应幂等: %v", err)
	}

	// 旧代际不得释放新占用。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		l, err := AcquireNodeSlot(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_bbbbbb", RunID: "run_bbb", Generation: 2,
		}, "n1")
		if err != nil {
			return err
		}
		if l.ResourceKey != "node:n1#1" {
			t.Fatalf("槽位应复用同一资源键，得到 %q", l.ResourceKey)
		}
		return nil
	})
	if err := WithTx(path, func(tx *sql.Tx) error {
		return ReleaseByResource(tx, "node:n1#1", 1, "stale release", ReleaseCondition{ProcessStopConfirmed: true})
	}); !hasCode(err, model.CodeReleaseGeneration) {
		t.Fatalf("旧代际释放新占用应报 E243，得到 %v", err)
	}
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		l, err := ActiveLease(tx, "node:n1#1")
		if err != nil {
			return err
		}
		if l == nil || l.Generation != 2 {
			t.Fatalf("新占用被旧代际放掉了: %+v", l)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestSlotAndWorkspaceStayIndependent 覆盖「空闲计算槽与待验收目录可分别保留」：
// 进程停稳后槽位可以立刻给别人用，目录仍被占着等验收。
func TestSlotAndWorkspaceStayIndependent(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	mustWithTx(t, path, func(tx *sql.Tx) error {
		// 账号额度故意设得比槽上限大：它不得被当成并发上限。
		return UpsertNode(tx, &Node{ID: "n1", SlotLimit: 2, AccountQuota: 99})
	})

	var slotA, wsA string
	mustWithTx(t, path, func(tx *sql.Tx) error {
		slot, err := AcquireNodeSlot(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_aaaaaa", RunID: "run_aaa", Generation: 1,
		}, "n1")
		if err != nil {
			return err
		}
		slotA = slot.ID
		ws, err := AcquireLease(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_aaaaaa", RunID: "run_aaa", Generation: 1,
		}, Resource{Kind: model.LeaseWorkspace, Path: "/tmp/ws/aaa"})
		if err != nil {
			return err
		}
		wsA = ws.ID
		return nil
	})

	// 产物未冻结：目录放不掉（E242）。
	if err := WithTx(path, func(tx *sql.Tx) error {
		return ReleaseLease(tx, wsA, 1, "release", ReleaseCondition{ProcessStopConfirmed: true})
	}); !hasCode(err, model.CodeWorkspaceNotFrozen) {
		t.Fatalf("未冻结产物应报 E242，得到 %v", err)
	}

	// 进程停稳：槽位可以放，目录不受影响。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		return ReleaseLease(tx, slotA, 1, "process stopped", ReleaseCondition{ProcessStopConfirmed: true})
	})
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		cap, err := Capacity(tx, "n1")
		if err != nil {
			return err
		}
		if cap.SlotLimit != 2 || cap.ActiveSlots != 0 || cap.FreeSlots != 2 {
			t.Fatalf("容量应按 slot_limit 而非账号额度: %+v", cap)
		}
		if cap.FreeSlots == 99 {
			t.Fatal("账号额度被当成槽位推导依据")
		}
		ws, err := GetLease(tx, wsA)
		if err != nil {
			return err
		}
		if ws.Released() {
			t.Fatal("释放槽位不该连带释放目录")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 槽位可以让给下一题，而上一题的目录还在等验收。
	var slotB string
	mustWithTx(t, path, func(tx *sql.Tx) error {
		slot, err := AcquireNodeSlot(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_bbbbbb", RunID: "run_bbb", Generation: 1,
		}, "n1")
		if err != nil {
			return err
		}
		slotB = slot.ResourceKey
		return nil
	})
	if slotB != "node:n1#1" {
		t.Fatalf("应复用最小空闲槽，得到 %q", slotB)
	}
	// 待验收的目录此时仍唯一持有者。
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		l, err := ActiveLease(tx, "ws:/tmp/ws/aaa")
		if err != nil {
			return err
		}
		if l == nil || l.InstanceID != "ti_aaaaaa" {
			t.Fatalf("目录占用丢了: %+v", l)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 产物冻结后目录才可释放。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		return ReleaseLease(tx, wsA, 1, "delivered", ReleaseCondition{DeliveryFrozen: true})
	})
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		l, err := ActiveLease(tx, "ws:/tmp/ws/aaa")
		if err != nil {
			return err
		}
		if l != nil {
			t.Fatalf("冻结后目录应已释放，仍有 %+v", l)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNodeSlotExhaustion(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	mustWithTx(t, path, func(tx *sql.Tx) error {
		return UpsertNode(tx, &Node{ID: "n1", SlotLimit: 2})
	})

	var keys []string
	for i := 0; i < 2; i++ {
		mustWithTx(t, path, func(tx *sql.Tx) error {
			l, err := AcquireNodeSlot(tx, LeaseSpec{
				ProjectID: testProject, InstanceID: "ti_aaaaaa", RunID: "run_aaa", Generation: 1,
			}, "n1")
			if err != nil {
				return err
			}
			keys = append(keys, l.ResourceKey)
			return nil
		})
	}
	if keys[0] != "node:n1#1" || keys[1] != "node:n1#2" {
		t.Fatalf("应从最小槽号开始: %v", keys)
	}

	if err := WithTx(path, func(tx *sql.Tx) error {
		_, err := AcquireNodeSlot(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_cccccc", Generation: 1,
		}, "n1")
		return err
	}); !hasPort(err, model.PortErrCapacityExhausted) {
		t.Fatalf("槽位占满应报 capacity_exhausted，得到 %v", err)
	}

	// 放掉 1 号槽后应重新可用。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		return ReleaseByResource(tx, "node:n1#1", 1, "process stopped", ReleaseCondition{ProcessStopConfirmed: true})
	})
	mustWithTx(t, path, func(tx *sql.Tx) error {
		l, err := AcquireNodeSlot(tx, LeaseSpec{
			ProjectID: testProject, InstanceID: "ti_cccccc", Generation: 2,
		}, "n1")
		if err != nil {
			return err
		}
		if l.ResourceKey != "node:n1#1" {
			t.Fatalf("应补回 1 号槽，得到 %q", l.ResourceKey)
		}
		return nil
	})
}
