package notifications_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/notifications"
)

// TestRaiseDedupesByEventAndChannel 对应验收第一条的前半句「按业务事件和渠道去重」：
// 同一业务事件重放不会产生第二条记录，每个渠道各自一条。
func TestRaiseDedupesByEventAndChannel(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)
	ev := runEvent("evt_wait_1", model.PhaseWaiting)

	first := raise(t, store, svc, ev)
	if len(first.Created) != 2 {
		t.Fatalf("waiting 默认应落 in_app 与 desktop 两条，得到 %d：%+v", len(first.Created), first.Created)
	}
	seen := map[notifications.Channel]bool{}
	for _, n := range first.Created {
		if seen[n.Channel] {
			t.Fatalf("渠道 %s 重复创建", n.Channel)
		}
		seen[n.Channel] = true
		if n.Status != notifications.StatusPending {
			t.Fatalf("新提醒应为 pending，得到 %s", n.Status)
		}
		if n.Kind != notifications.KindWaiting {
			t.Fatalf("waiting 阶段应归为 waiting 类型，得到 %s", n.Kind)
		}
	}
	if !seen[notifications.ChannelInApp] || !seen[notifications.ChannelDesktop] {
		t.Fatalf("默认策略应覆盖站内与桌面两个渠道，实际 %+v", seen)
	}

	second := raise(t, store, svc, ev)
	if len(second.Created) != 0 {
		t.Fatalf("同一业务事件重放不得新建提醒，得到 %d 条", len(second.Created))
	}
	if len(second.Skipped) != 2 {
		t.Fatalf("两个渠道都应被压掉，得到 %d", len(second.Skipped))
	}
	for _, sk := range second.Skipped {
		if sk.Reason != notifications.SkipDuplicate {
			t.Fatalf("重放应压成 duplicate，得到 %s", sk.Reason)
		}
	}
	if n := countRows(t, store); n != 2 {
		t.Fatalf("库里应只有 2 条记录，得到 %d", n)
	}
}

// TestRaiseSuppressesUnchangedState 对应验收第一条的后半句「默认不为无变化状态持续打扰」：
// 同一对象停在同一状态、且上一条还没被确认时，再来事件也不打扰；
// 但状态真的变了就必须提醒——否则「别重复」会退化成「别提醒」。
func TestRaiseSuppressesUnchangedState(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)

	raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))

	// 同一任务另一次 Run 也停在 waiting：这是同一件事，不该第二次打扰。
	same := raise(t, store, svc, runEvent("evt_wait_2", model.PhaseWaiting))
	if len(same.Created) != 0 {
		t.Fatalf("状态未变不得新建提醒，得到 %d 条", len(same.Created))
	}
	for _, sk := range same.Skipped {
		if sk.Reason != notifications.SkipUnchanged {
			t.Fatalf("状态未变应压成 unchanged，得到 %s", sk.Reason)
		}
	}

	// 状态变成 failed：这是新情况，必须提醒。
	changed := raise(t, store, svc, runEvent("evt_fail_1", model.PhaseFailed))
	if len(changed.Created) != 2 {
		t.Fatalf("状态变化应产生新提醒，得到 %d 条", len(changed.Created))
	}
	for _, n := range changed.Created {
		if n.Kind != notifications.KindFailed {
			t.Fatalf("failed 阶段应归为 failed 类型，得到 %s", n.Kind)
		}
	}
	if n := countRows(t, store); n != 4 {
		t.Fatalf("应累计 4 条（waiting 2 + failed 2），得到 %d", n)
	}
}

// TestRaiseRenotifiesAfterAckOnlyOutsideWindow 钉住「刚处理过就别再弹，隔久了再弹」：
// 已确认记录只在去重窗口内压制同一状态，窗口外再次发生算新的一次介入需求。
func TestRaiseRenotifiesAfterAckOnlyOutsideWindow(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)

	first := raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	for _, n := range first.Created {
		if !ack(t, store, svc, n.ID, clk.now()) {
			t.Fatalf("首次确认应生效：%s", n.ID)
		}
	}

	// 窗口内：用户刚处理完，同一状态再来不打扰。
	inWindow := raise(t, store, svc, runEvent("evt_wait_2", model.PhaseWaiting))
	if len(inWindow.Created) != 0 {
		t.Fatalf("去重窗口内不得重复提醒，得到 %d 条", len(inWindow.Created))
	}
	for _, sk := range inWindow.Skipped {
		if sk.Reason != notifications.SkipSettled {
			t.Fatalf("窗口内已确认应压成 settled，得到 %s", sk.Reason)
		}
	}

	// 窗口外：同一状态再次发生是新的一次介入需求。
	clk.advance(notifications.DefaultDedupWindow + time.Minute)
	outWindow := raise(t, store, svc, runEvent("evt_wait_3", model.PhaseWaiting))
	if len(outWindow.Created) != 2 {
		t.Fatalf("窗口外应重新提醒，得到 %d 条", len(outWindow.Created))
	}
}

// TestRaiseSkipsEventsThatDoNotNeedUser 钉住规则边界：不卡人的事实一条都不落库。
func TestRaiseSkipsEventsThatDoNotNeedUser(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)

	cases := []notifications.BusinessEvent{
		runEvent("evt_done_1", model.PhaseCompleted),
		runEvent("evt_cancel_1", model.PhaseCancelled),
		taskEvent("evt_cancel_2", "ti_abc123", model.TaskCancelled),
		{
			EventID:   "evt_review_1",
			ProjectID: testProject,
			Kind:      notifications.EventDeliveryReview,
			Subject:   "dlv_abc123",
			Verdict:   model.VerdictNotRequired,
		},
	}
	for _, ev := range cases {
		res := raise(t, store, svc, ev)
		if len(res.Created) != 0 {
			t.Fatalf("%s/%s 不该提醒，却创建了 %d 条", ev.Kind, ev.State(), len(res.Created))
		}
		if len(res.Skipped) != 1 || res.Skipped[0].Reason != notifications.SkipNotNeeded {
			t.Fatalf("%s/%s 应压成 not_needed，得到 %+v", ev.Kind, ev.State(), res.Skipped)
		}
	}
	if n := countRows(t, store); n != 0 {
		t.Fatalf("不该提醒的事实不得落库，得到 %d 条", n)
	}
}

// TestRaiseCoversDeliveryChanges 确认「交付变化」确实会提醒（F28 的待验收／完成／驳回）。
func TestRaiseCoversDeliveryChanges(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)

	cases := []notifications.BusinessEvent{
		taskEvent("evt_review_1", "ti_abc123", model.TaskInReview),
		taskEvent("evt_done_1", "ti_abc123", model.TaskDone),
		{
			EventID:   "evt_verdict_1",
			ProjectID: testProject,
			Kind:      notifications.EventDeliveryReview,
			Subject:   "dlv_abc123",
			Verdict:   model.VerdictChangesRequired,
		},
	}
	for _, ev := range cases {
		res := raise(t, store, svc, ev)
		if len(res.Created) != 1 {
			t.Fatalf("%s/%s 应只落站内一条，得到 %d", ev.Kind, ev.State(), len(res.Created))
		}
		if res.Created[0].Kind != notifications.KindDelivery {
			t.Fatalf("%s/%s 应归为 delivery，得到 %s", ev.Kind, ev.State(), res.Created[0].Kind)
		}
		if res.Created[0].Channel != notifications.ChannelInApp {
			t.Fatalf("delivery 默认只走站内，得到 %s", res.Created[0].Channel)
		}
	}
}

// TestPolicyControlsKinds 对应 F33「可配置类型」：关掉的类型不提醒，改过的渠道按配置走。
func TestPolicyControlsKinds(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)
	svc.Policy = notifications.DefaultPolicy().Without(notifications.KindDelivery)

	res := raise(t, store, svc, taskEvent("evt_review_1", "ti_abc123", model.TaskInReview))
	if len(res.Created) != 0 {
		t.Fatalf("关掉的类型不得提醒，得到 %d 条", len(res.Created))
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != notifications.SkipNotConfigured {
		t.Fatalf("关掉的类型应压成 not_configured，得到 %+v", res.Skipped)
	}

	// 只留桌面渠道。
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelDesktop)
	res = raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	if len(res.Created) != 1 || res.Created[0].Channel != notifications.ChannelDesktop {
		t.Fatalf("应只落桌面渠道，得到 %+v", res.Created)
	}
}

// TestPolicyRejectsUnknownChannel 是配置校验：渠道必须在封闭集合内，重复配置也算错。
func TestPolicyRejectsUnknownChannel(t *testing.T) {
	bad := notifications.Policy{Rules: map[notifications.Kind]notifications.Rule{
		notifications.KindWaiting: {Channels: []notifications.Channel{"pager"}},
	}}
	if errs := bad.Validate(); len(errs) == 0 {
		t.Fatal("未登记的渠道应被拒绝")
	}

	dup := notifications.Policy{Rules: map[notifications.Kind]notifications.Rule{
		notifications.KindWaiting: {Channels: []notifications.Channel{
			notifications.ChannelInApp, notifications.ChannelInApp,
		}},
	}}
	if errs := dup.Validate(); len(errs) == 0 {
		t.Fatal("重复渠道应被拒绝")
	}

	if errs := notifications.DefaultPolicy().Validate(); len(errs) != 0 {
		t.Fatalf("默认策略必须自洽，得到 %v", errs)
	}
}

// TestRaiseRejectsIncompleteEvent 钉住「没有对象就没有状态未变可判」：
// 缺 Subject 的业务事实必须报错，而不是退化成「每来一次弹一次」。
func TestRaiseRejectsIncompleteEvent(t *testing.T) {
	store, _ := newStore(t)
	svc := newService(newClock())

	cases := []notifications.BusinessEvent{
		{EventID: "evt_x1", ProjectID: testProject, Kind: notifications.EventRunPhase, Phase: model.PhaseWaiting},
		{EventID: "", ProjectID: testProject, Kind: notifications.EventRunPhase, Subject: "ti_abc123", Phase: model.PhaseWaiting},
		{EventID: "evt_x2", ProjectID: "not-a-project", Kind: notifications.EventRunPhase, Subject: "ti_abc123", Phase: model.PhaseWaiting},
		{EventID: "evt_x3", ProjectID: testProject, Kind: notifications.EventRunPhase, Subject: "ti_abc123", Phase: "sleeping"},
	}
	for i, ev := range cases {
		if err := raiseErr(store, svc, ev); err == nil {
			t.Fatalf("第 %d 条残缺事件应被拒绝", i+1)
		}
	}
	if n := countRows(t, store); n != 0 {
		t.Fatalf("被拒绝的事件不得落库，得到 %d 条", n)
	}
}

// TestUnhandledIncludesDelivered 钉住「delivered 不等于处理完」：
// 投递成功但没确认的记录必须留在未处理列表里，否则用户会以为事情过去了。
func TestUnhandledIncludesDelivered(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelInApp)

	res := raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	id := res.Created[0].ID

	att := begin(t, store, svc, id)
	done := complete(t, store, svc, id, att.No, notifications.Outcome{Delivered: true, Evidence: "popup:1"})
	if done.Status != notifications.StatusDelivered {
		t.Fatalf("投递成功应为 delivered，得到 %s", done.Status)
	}
	if done.Evidence != "popup:1" {
		t.Fatalf("投递证据应落库，得到 %q", done.Evidence)
	}

	list := unhandled(t, store, svc)
	if len(list) != 1 {
		t.Fatalf("delivered 未确认仍在未处理列表，期望 1 条，得到 %d", len(list))
	}
	if list[0].Status != notifications.StatusDelivered {
		t.Fatalf("状态应为 delivered，得到 %s", list[0].Status)
	}

	// 确认后列表清空；重复确认幂等。
	if !ack(t, store, svc, id, clk.now()) {
		t.Fatal("首次确认应生效")
	}
	if ack(t, store, svc, id, clk.now()) {
		t.Fatal("重复确认应幂等返回 false")
	}
	if list := unhandled(t, store, svc); len(list) != 0 {
		t.Fatalf("确认后未处理列表应清空，得到 %d 条", len(list))
	}
}

// TestAckDuringFlightWinsOverDeliveryResult 钉住竞态：用户点了确认之后，
// 在途投递的结果不能把 acked 降回 delivered。
func TestAckDuringFlightWinsOverDeliveryResult(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)
	svc.Policy = notifications.DefaultPolicy().With(notifications.KindWaiting, notifications.ChannelDesktop)

	res := raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	id := res.Created[0].ID

	att := begin(t, store, svc, id)
	if !ack(t, store, svc, id, clk.now()) {
		t.Fatal("投递途中确认应生效")
	}
	done := complete(t, store, svc, id, att.No, notifications.Outcome{Delivered: true})
	if done.Status != notifications.StatusAcked {
		t.Fatalf("确认优先：状态应保持 acked，得到 %s", done.Status)
	}
	if done.OpenAttempt {
		t.Fatal("确认后不应留下未完成尝试")
	}

	// 已确认的记录不再接受新的投递意图。
	var err error
	if err = store.Write(func(tx *sql.Tx) error {
		_, e := svc.BeginAttempt(tx, id)
		return e
	}); !errors.Is(err, notifications.ErrAlreadyHandled) {
		t.Fatalf("已确认记录应返回 ErrAlreadyHandled，得到 %v", err)
	}
}

// TestDedupWindowCountsFromAck 钉住去重窗口的起点：从用户确认那一刻算，
// 而不是从提醒登记那一刻算。晚处理的人不该被更早地再次打扰。
func TestDedupWindowCountsFromAck(t *testing.T) {
	store, _ := newStore(t)
	clk := newClock()
	svc := newService(clk)

	first := raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	// 用户拖了 20 分钟才确认。
	clk.advance(20 * time.Minute)
	for _, n := range first.Created {
		if !ack(t, store, svc, n.ID, clk.now()) {
			t.Fatalf("确认应生效：%s", n.ID)
		}
	}

	// 再过 20 分钟：距登记已 40 分钟（超窗），距确认只有 20 分钟（仍在窗内）。
	clk.advance(20 * time.Minute)
	res := raise(t, store, svc, runEvent("evt_wait_2", model.PhaseWaiting))
	if len(res.Created) != 0 {
		t.Fatalf("去重窗口应从确认时间起算，此时仍在窗内，却新建了 %d 条", len(res.Created))
	}
	for _, sk := range res.Skipped {
		if sk.Reason != notifications.SkipSettled {
			t.Fatalf("应压成 settled，得到 %s", sk.Reason)
		}
	}
}
