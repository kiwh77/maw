package notifications_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/notifications"
)

// TestDispatchDeliversEachPendingOnce 钉住「未处理」与「待投递」是两个集合：
// delivered 仍在未处理列表里等确认，但绝不能再投一次。
func TestDispatchDeliversEachPendingOnce(t *testing.T) {
	store, _ := newStore(t)
	svc := newService(newClock())
	rec := &recorder{}
	svc.Deliverers[notifications.ChannelDesktop] = rec

	raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))

	rep := dispatch(t, store, svc)
	if rep.Delivered != 2 {
		t.Fatalf("站内与桌面各一条都应记为投递成功，得到 %+v", rep)
	}
	if len(rec.requests) != 1 {
		t.Fatalf("只有桌面渠道需要外部投递，应调用 1 次，得到 %d", len(rec.requests))
	}
	if rec.requests[0].Channel != notifications.ChannelDesktop {
		t.Fatalf("外部投递渠道应为 desktop，得到 %s", rec.requests[0].Channel)
	}
	if rec.requests[0].Attempt != 1 || rec.requests[0].DuplicateRisk {
		t.Fatalf("首次投递应是一次无重复风险的尝试：%+v", rec.requests[0])
	}

	// 未处理仍有 2 条（投递成功 ≠ 用户处理）。
	if list := unhandled(t, store, svc); len(list) != 2 {
		t.Fatalf("delivered 未确认应留在未处理列表，得到 %d 条", len(list))
	}

	// 再跑一轮：不该重复弹窗。
	again := dispatch(t, store, svc)
	if again.Delivered != 0 || again.Failed != 0 || again.Ambiguous != 0 {
		t.Fatalf("已投递的记录不得重复投递，得到 %+v", again)
	}
	if len(rec.requests) != 1 {
		t.Fatalf("重复轮询不得再次调用投递实现，累计调用 %d 次", len(rec.requests))
	}
}

// TestDispatchSkipsChannelWithoutDeliverer 钉住「没有实现不算失败」：
// 接入层还没接上桌面托盘时，记录应留在 pending 等它，而不是被判失败。
func TestDispatchSkipsChannelWithoutDeliverer(t *testing.T) {
	store, _ := newStore(t)
	svc := newService(newClock())
	svc.Deliverers = nil

	raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))

	rep := dispatch(t, store, svc)
	if rep.Delivered != 1 || rep.Skipped != 1 {
		t.Fatalf("站内应记为投递成功、桌面应跳过，得到 %+v", rep)
	}
	list := unhandled(t, store, svc)
	if len(list) != 2 {
		t.Fatalf("两条都该留在未处理，得到 %d", len(list))
	}
	for _, n := range list {
		if n.Channel == notifications.ChannelDesktop && n.Status != notifications.StatusPending {
			t.Fatalf("没有投递实现的渠道应保持 pending，得到 %s", n.Status)
		}
	}
}

// TestDispatchTreatsUnjudgeableDeliveryAsAmbiguous 是「非原子投递」的另一半：
// 弹窗调用超时、托盘没回执时，既不能判成功也不能判失败，必须留下重复风险。
func TestDispatchTreatsUnjudgeableDeliveryAsAmbiguous(t *testing.T) {
	store, _ := newStore(t)
	svc := newService(newClock())
	rec := &recorder{err: &notifications.AmbiguousDeliveryError{Reason: "弹窗调用超时"}}
	svc.Deliverers[notifications.ChannelDesktop] = rec

	raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))

	rep := dispatch(t, store, svc)
	if rep.Ambiguous != 1 || rep.Delivered != 1 {
		t.Fatalf("桌面应记为不可判定、站内应成功，得到 %+v", rep)
	}

	list := unhandled(t, store, svc)
	var desktop notifications.Notification
	for _, n := range list {
		if n.Channel == notifications.ChannelDesktop {
			desktop = n
		}
	}
	if desktop.ID == "" {
		t.Fatal("不可判定的记录必须留在未处理列表里")
	}
	if desktop.Status != notifications.StatusPending {
		t.Fatalf("不可判定不得判成 delivered 或 failed，得到 %s", desktop.Status)
	}
	if !desktop.DuplicateRisk || desktop.AmbiguousAttempts != 1 {
		t.Fatalf("必须留下重复风险与中断次数：%+v", desktop)
	}
	if !strings.Contains(desktop.LastError, "不可判定") {
		t.Fatalf("应记下不可判定的原因，得到 %q", desktop.LastError)
	}

	// 下一轮重投时，实现方必须知道可能重复。
	rec.err = nil
	rep2 := dispatch(t, store, svc)
	if rep2.Delivered != 1 {
		t.Fatalf("恢复投递应成功，得到 %+v", rep2)
	}
	last := rec.requests[len(rec.requests)-1]
	if !last.DuplicateRisk || last.Attempt != 2 {
		t.Fatalf("重投应带重复风险且是第 2 次尝试：%+v", last)
	}
}

// TestDispatchMarksHardFailureAsFailed 钉住普通失败与不可判定的区别：
// 明确报错的投递记为 failed，仍可重投，但不产生重复风险。
func TestDispatchMarksHardFailureAsFailed(t *testing.T) {
	store, _ := newStore(t)
	svc := newService(newClock())
	rec := &recorder{err: errors.New("托盘不可用")}
	svc.Deliverers[notifications.ChannelDesktop] = rec

	raise(t, store, svc, runEvent("evt_wait_1", model.PhaseWaiting))
	rep := dispatch(t, store, svc)
	if rep.Failed != 1 || rep.Ambiguous != 0 {
		t.Fatalf("明确失败应记 failed 而不是 ambiguous，得到 %+v", rep)
	}

	var failed notifications.Notification
	for _, n := range unhandled(t, store, svc) {
		if n.Channel == notifications.ChannelDesktop {
			failed = n
		}
	}
	if failed.Status != notifications.StatusFailed {
		t.Fatalf("状态应为 failed，得到 %s", failed.Status)
	}
	if failed.DuplicateRisk {
		t.Fatal("明确失败说明没弹成功，不该带重复风险")
	}
	if !strings.Contains(failed.LastError, "托盘不可用") {
		t.Fatalf("应保留失败原因，得到 %q", failed.LastError)
	}

	// 失败记录仍在待投递集合里：下一轮会重试。
	rec.err = nil
	if rep2 := dispatch(t, store, svc); rep2.Delivered != 1 {
		t.Fatalf("失败记录应可重投，得到 %+v", rep2)
	}
}

// TestDispatchRejectsNilStore 确认非法调用会报错而不是 panic。
func TestDispatchRejectsNilStore(t *testing.T) {
	svc := newService(newClock())
	if _, err := svc.Dispatch(nil, nil, testProject); err == nil {
		t.Fatal("缺少事务入口应报错")
	}
}
