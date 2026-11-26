package model_test

import (
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
)

func TestInvariantRegistryIsCompleteAndUnique(t *testing.T) {
	all := model.AllCodes()
	if len(all) != 85 {
		t.Fatalf("冻结登记表共 85 条（E1=18、E2=26、E3=23、P1=18），实际 %d 条", len(all))
	}
	seen := map[model.Code]bool{}
	for _, c := range all {
		if seen[c] {
			t.Fatalf("错误码重复定义：%s", c)
		}
		seen[c] = true
		if !c.Known() {
			t.Fatalf("%s 未登记", c)
		}
		if c.Title() == "" {
			t.Fatalf("%s 缺标题", c)
		}
	}
}

func TestPortErrorClassesMatchContract(t *testing.T) {
	// 契约 §4 直接举例的分类，必须逐条对齐。
	cases := []struct {
		code model.PortErrorCode
		want model.Class
	}{
		{model.PortErrBusy, model.ClassRetryable},
		{model.PortErrProbeTimeout, model.ClassRetryable},
		{model.PortErrConflict, model.ClassConflict},
		{model.PortErrRevisionMismatch, model.ClassConflict},
		{model.PortErrGenerationMismatch, model.ClassConflict},
		{model.PortErrNoReady, model.ClassTerminal},
		{model.PortErrInvalidSpec, model.ClassTerminal},
		{model.PortErrCapabilityMissing, model.ClassTerminal},
		{model.PortErrPlatformUnsupported, model.ClassTerminal},
		{model.PortErrProtocolUnknown, model.ClassUnknown},
		{model.PortErrStopUnconfirmed, model.ClassUnknown},
		{model.PortErrVersionUnsupported, model.ClassUnknown},
	}
	for _, c := range cases {
		if got := c.code.ClassOf(); got != c.want {
			t.Errorf("%s 分类应为 %s，实际 %s", c.code, c.want, got)
		}
	}
}

func TestEveryDeclaredPortErrorIsClassified(t *testing.T) {
	codes := model.AllPortErrorCodes()
	if len(codes) != 26 {
		t.Fatalf("端口错误码应登记 26 个，实际 %d 个", len(codes))
	}
	for _, c := range codes {
		if !c.Known() {
			t.Fatalf("%s 未登记分类", c)
		}
		if !c.ClassOf().Valid() {
			t.Fatalf("%s 分类非法：%s", c, c.ClassOf())
		}
	}
	// 未登记的码不得被当成成功：默认 unknown 保持现场。
	var unregistered model.PortErrorCode = "some_new_thing"
	if unregistered.ClassOf() != model.ClassUnknown {
		t.Fatal("未登记端口错误必须落 unknown")
	}
}

func TestStopUnconfirmedIsNotFailure(t *testing.T) {
	err := model.PortFailure(model.PortErrStopUnconfirmed, "进程未确认退出")
	if err.Class != model.ClassUnknown {
		t.Fatalf("stop_unconfirmed 属 unknown，收到 %s", err.Class)
	}
	if model.HasPortError([]*model.Error{err}, model.PortErrStopUnconfirmed) == false {
		t.Fatal("应按端口错误码检索")
	}
}

func TestErrAggregation(t *testing.T) {
	if model.Err(nil) != nil {
		t.Fatal("空违反集合应返回 nil")
	}
	single := model.Violation(model.CodeDepSelf, "deps[0]", "自依赖")
	if model.Err([]*model.Error{single}) != single {
		t.Fatal("单条违反应原样返回")
	}
}

func TestNoBusinessStatusVocabularyDrift(t *testing.T) {
	// 业务任务状态沿用现有词汇，一个不多一个不少。
	want := []model.TaskStatus{
		"backlog", "ready", "claimed", "in_progress", "self_check",
		"in_review", "merging", "conflict", "blocked", "done", "cancelled",
	}
	got := model.AllTaskStatuses()
	if len(got) != len(want) {
		t.Fatalf("任务状态应有 %d 个，实际 %d 个", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个状态漂移：want %s got %s", i, want[i], got[i])
		}
	}
	if len(model.AllRunPhases()) != 10 {
		t.Fatal("运行阶段必须是十个封闭值")
	}
}
