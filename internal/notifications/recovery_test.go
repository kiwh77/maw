package notifications_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/notifications"
)

// TestRecoverMarksInterruptedAttemptAmbiguous 对应验收第二条：
// 重启后能恢复未处理提醒，且 OS 弹窗非原子投递的重复边界是可见的。
//
// 现场构造方式就是真实崩溃留下的样子——记了投递意图、没记结果、进程没了。
// 不 mock 崩溃，直接把库关掉再打开，让「重启」这件事真的发生。
func TestRecoverMarksInterruptedAttemptAmbiguous(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.db")
	store := openAt(t, path)
	clk := newClock()
	svc := newService(clk)
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelDesktop)

	res := raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	id := res.Created[0].ID
	att := begin(t, store, svc, id)
	if att.No != 1 {
		t.Fatalf("首次尝试序号应为 1，得到 %d", att.No)
	}
	if att.Request.DuplicateRisk {
		t.Fatal("首次投递不该带重复风险")
	}

	// 进程死在这里：意图已落库，结果没有。
	if err := store.Close(); err != nil {
		t.Fatalf("关库失败：%v", err)
	}
	store = openAt(t, path)
	defer func() { _ = store.Close() }()

	svc2 := newService(clk)
	rep := recoverAll(t, store, svc2)
	if rep.Scanned != 1 {
		t.Fatalf("应扫到 1 条未处理记录，得到 %d", rep.Scanned)
	}
	if len(rep.Ambiguous) != 1 {
		t.Fatalf("中断的投递应被标成 ambiguous，得到 %d 条", len(rep.Ambiguous))
	}
	got := rep.Ambiguous[0]
	if got.ID != id {
		t.Fatalf("ambiguous 记录身份不对：%s != %s", got.ID, id)
	}
	if !got.DuplicateRisk {
		t.Fatal("中断过的提醒必须标出重复风险：再弹一次用户可能看到第二遍")
	}
	if got.AmbiguousAttempts != 1 {
		t.Fatalf("应记下 1 次不可判定尝试，得到 %d", got.AmbiguousAttempts)
	}
	if got.Status != notifications.StatusPending {
		t.Fatalf("中断不等于投递成功，状态应留在 pending，得到 %s", got.Status)
	}
	if got.OpenAttempt {
		t.Fatal("恢复后不应再留未完成尝试")
	}
	if len(rep.Resumable) != 0 || len(rep.AwaitingAck) != 0 {
		t.Fatalf("同一条记录不得同时落进其它桶：resumable=%d awaiting=%d",
			len(rep.Resumable), len(rep.AwaitingAck))
	}

	// 恢复后重投：必须带重复标记，尝试序号继续增长。
	rec := &recorder{}
	svc2.Deliverers = map[notifications.Channel]notifications.Deliverer{
		notifications.ChannelDesktop: rec,
	}
	drep := dispatch(t, store, svc2)
	if drep.Delivered != 1 || drep.Ambiguous != 0 {
		t.Fatalf("恢复后应成功投递 1 条，得到 %+v", drep)
	}
	if len(rec.requests) != 1 {
		t.Fatalf("应调用投递实现 1 次，得到 %d", len(rec.requests))
	}
	if !rec.requests[0].DuplicateRisk {
		t.Fatal("重投必须告诉实现方「可能已经弹过」")
	}
	if rec.requests[0].Attempt != 2 {
		t.Fatalf("重投应是第 2 次尝试，得到 %d", rec.requests[0].Attempt)
	}

	// 再跑一次恢复：不该再出现 ambiguous，且这条已进入「等确认」。
	rep2 := recoverAll(t, store, svc2)
	if len(rep2.Ambiguous) != 0 {
		t.Fatalf("已收尾的尝试不该再报 ambiguous，得到 %d 条", len(rep2.Ambiguous))
	}
	if len(rep2.AwaitingAck) != 1 {
		t.Fatalf("投递成功的记录应进入等确认桶，得到 %d 条", len(rep2.AwaitingAck))
	}
	if rep2.AwaitingAck[0].AmbiguousAttempts != 1 || !rep2.AwaitingAck[0].DuplicateRisk {
		t.Fatal("重复风险是历史事实，投递成功后也不能抹掉")
	}
}

// TestRecoverSeparatesResumableAndAwaitingAck 钉住恢复的三个桶互不重叠：
// 从没投过的、投成功等确认的、投失败要重试的，处理方式完全不同。
func TestRecoverSeparatesResumableAndAwaitingAck(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelInApp)

	never := raise(t, store, svc, taskEvent("evt_a", "ti_aaa111", model.TaskBlocked)).Created[0]
	delivered := raise(t, store, svc, taskEvent("evt_b", "ti_bbb222", model.TaskBlocked)).Created[0]
	failed := raise(t, store, svc, taskEvent("evt_c", "ti_ccc333", model.TaskBlocked)).Created[0]
	handled := raise(t, store, svc, taskEvent("evt_d", "ti_ddd444", model.TaskBlocked)).Created[0]

	attB := begin(t, store, svc, delivered.ID)
	complete(t, store, svc, delivered.ID, attB.No, notifications.Outcome{Delivered: true})

	attC := begin(t, store, svc, failed.ID)
	complete(t, store, svc, failed.ID, attC.No, notifications.Outcome{Err: "托盘不可用"})

	if !ack(t, store, svc, handled.ID, clk.now()) {
		t.Fatal("确认应生效")
	}

	rep := recoverAll(t, store, svc)
	if len(rep.Ambiguous) != 0 {
		t.Fatalf("没有中断尝试，不该有 ambiguous，得到 %d 条", len(rep.Ambiguous))
	}
	if len(rep.Resumable) != 2 {
		t.Fatalf("未投递与投递失败的应进 resumable，得到 %d 条", len(rep.Resumable))
	}
	if len(rep.AwaitingAck) != 1 || rep.AwaitingAck[0].ID != delivered.ID {
		t.Fatalf("投递成功的应单独进 awaiting_ack，得到 %+v", rep.AwaitingAck)
	}
	if rep.Scanned != 3 {
		t.Fatalf("已确认的记录不参与恢复，扫描数应为 3，得到 %d", rep.Scanned)
	}
	for _, n := range rep.Resumable {
		if n.ID == never.ID || n.ID == failed.ID {
			continue
		}
		t.Fatalf("resumable 里出现了不该出现的记录 %s", n.ID)
	}
}

// TestRecoverWithoutInterruptionIsQuiet 确认恢复不会凭空造出 ambiguous：
// 正常重启（没有在途投递）只做分类，不改任何记录。
func TestRecoverWithoutInterruptionIsQuiet(t *testing.T) {
	store, _ := newStore(t)
	svc := newService(newClock())
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelInApp)

	res := raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	rep := recoverAll(t, store, svc)
	if len(rep.Ambiguous) != 0 {
		t.Fatalf("没有在途投递就不该有 ambiguous，得到 %d 条", len(rep.Ambiguous))
	}
	got := rep.Resumable[0]
	if got.DuplicateRisk || got.AmbiguousAttempts != 0 {
		t.Fatalf("未投递过的提醒不该带重复风险：%+v", got)
	}
	if got.ID != res.Created[0].ID {
		t.Fatalf("恢复应保留原身份，得到 %s", got.ID)
	}
}

// TestBeginAttemptCountsInterruptionWithoutRecover 钉住更隐蔽的一条：
// 上一次尝试没收尾就再投一次，本身也算一次中断，不能把两次尝试在账上合成一次。
func TestBeginAttemptCountsInterruptionWithoutRecover(t *testing.T) {
	store, _ := newStore(t)
	svc := newService(newClock())
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelDesktop)

	res := raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	id := res.Created[0].ID

	begin(t, store, svc, id)
	second := begin(t, store, svc, id)
	if second.No != 2 {
		t.Fatalf("第二次尝试序号应为 2，得到 %d", second.No)
	}
	if !second.Request.DuplicateRisk {
		t.Fatal("上一次没收尾就重投，必须标出重复风险")
	}
	done := complete(t, store, svc, id, second.No, notifications.Outcome{Delivered: true})
	if done.AmbiguousAttempts != 1 {
		t.Fatalf("应记下 1 次中断，得到 %d", done.AmbiguousAttempts)
	}
	if done.Attempts != 2 {
		t.Fatalf("应记 2 次尝试，得到 %d", done.Attempts)
	}
}

// TestCompleteAttemptRejectsStaleAttempt 钉住并发防线：
// 旧尝试的结果不能覆盖新尝试的记录，否则「第 1 次弹失败」会盖掉「第 2 次弹成功」。
func TestCompleteAttemptRejectsStaleAttempt(t *testing.T) {
	store, _ := newStore(t)
	svc := newService(newClock())
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelDesktop)

	res := raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	id := res.Created[0].ID

	first := begin(t, store, svc, id)
	second := begin(t, store, svc, id)
	if second.No != first.No+1 {
		t.Fatalf("第二次尝试序号应递增：%d → %d", first.No, second.No)
	}

	err := store.Write(func(tx *sql.Tx) error {
		_, e := svc.CompleteAttempt(tx, id, first.No, notifications.Outcome{Delivered: true})
		return e
	})
	if !errors.Is(err, notifications.ErrAttemptMismatch) {
		t.Fatalf("旧尝试的结果应被拒绝，得到 %v", err)
	}

	// 新尝试的结果照常写入。
	done := complete(t, store, svc, id, second.No, notifications.Outcome{Delivered: true})
	if done.Status != notifications.StatusDelivered {
		t.Fatalf("新尝试结果应生效，得到 %s", done.Status)
	}
}
