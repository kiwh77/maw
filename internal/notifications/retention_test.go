package notifications_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/notifications"
)

// TestPruneRemovesOnlyAckedBeyondRetention 钉住保留策略的边界：
// 只清「已确认且确认时间早于保留期」的记录。未处理（pending／delivered／failed）
// 一条都不能被清——提醒被清理策略吃掉，比多留几天严重得多。
func TestPruneRemovesOnlyAckedBeyondRetention(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelInApp)

	handled := raise(t, store, svc, taskEvent("evt_a", "ti_aaa111", model.TaskBlocked)).Created[0]
	pending := raise(t, store, svc, taskEvent("evt_b", "ti_bbb222", model.TaskBlocked)).Created[0]
	delivered := raise(t, store, svc, taskEvent("evt_c", "ti_ccc333", model.TaskBlocked)).Created[0]
	failed := raise(t, store, svc, taskEvent("evt_d", "ti_ddd444", model.TaskBlocked)).Created[0]

	attC := begin(t, store, svc, delivered.ID)
	complete(t, store, svc, delivered.ID, attC.No, notifications.Outcome{Delivered: true})
	attD := begin(t, store, svc, failed.ID)
	complete(t, store, svc, failed.ID, attD.No, notifications.Outcome{Err: "托盘不可用"})
	if !ack(t, store, svc, handled.ID, clk.now()) {
		t.Fatal("确认应生效")
	}

	// 保留期没过：什么都不该删。
	early := prune(t, store, svc)
	if early.Removed != 0 {
		t.Fatalf("保留期内不得删除，删了 %d 条", early.Removed)
	}

	clk.advance(notifications.DefaultRetentionAcked + time.Hour)
	late := prune(t, store, svc)
	if late.Removed != 1 {
		t.Fatalf("只该删掉 1 条已确认记录，删了 %d 条", late.Removed)
	}
	if late.Kept != 3 {
		t.Fatalf("未处理的三条必须留下，剩 %d 条", late.Kept)
	}
	if late.Cutoff == "" {
		t.Fatal("应给出本次清理的时间下界，便于事后核对")
	}

	// 被清掉的是确认过的那条，其余身份都还在。
	list := unhandled(t, store, svc)
	ids := map[string]bool{}
	for _, n := range list {
		ids[n.ID] = true
	}
	for _, want := range []notifications.Notification{pending, delivered, failed} {
		if !ids[want.ID] {
			t.Fatalf("未处理记录 %s 不该被清理", want.ID)
		}
	}
	if ids[handled.ID] {
		t.Fatal("已确认记录应已被清理")
	}
}

// TestPruneKeepsAmbiguousForever 钉住「不确定的提醒不能消失」：
// 中断过的投递可能已经弹过、也可能没弹过，它在用户确认之前必须一直留着。
func TestPruneKeepsAmbiguousForever(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelDesktop)

	res := raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	id := res.Created[0].ID
	begin(t, store, svc, id) // 记了意图，没有结果
	rep := recoverAll(t, store, svc)
	if len(rep.Ambiguous) != 1 {
		t.Fatalf("应恢复出 1 条 ambiguous，得到 %d", len(rep.Ambiguous))
	}

	// 时间推得很远，仍然不能清。
	clk.advance(365 * 24 * time.Hour)
	out := prune(t, store, svc)
	if out.Removed != 0 {
		t.Fatalf("未确认的提醒不得被保留策略删除，删了 %d 条", out.Removed)
	}
	if out.Kept != 1 {
		t.Fatalf("应保留 1 条，剩 %d 条", out.Kept)
	}
}

// TestPruneScopesToProject 确认清理范围按工程隔离，空工程表示全部。
func TestPruneScopesToProject(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelInApp)

	res := raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	if !ack(t, store, svc, res.Created[0].ID, clk.now()) {
		t.Fatal("确认应生效")
	}
	clk.advance(notifications.DefaultRetentionAcked + time.Hour)

	other := "fedcba9876543210"
	var scoped notifications.PruneReport
	if err := store.Write(func(tx *sql.Tx) error {
		var err error
		scoped, err = svc.Prune(tx, other)
		return err
	}); err != nil {
		t.Fatalf("按工程清理失败：%v", err)
	}
	if scoped.Removed != 0 {
		t.Fatalf("别的工程的记录不该被删，删了 %d 条", scoped.Removed)
	}

	all := prune(t, store, svc) // 空工程 = 全部
	if all.Removed != 1 {
		t.Fatalf("全量清理应删掉 1 条，删了 %d 条", all.Removed)
	}
}
