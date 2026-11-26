package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// TestObserverCannotControl 逐条覆盖本卡第二条验收：旁路上报拿不到任何控制权。
// 三条路径都必须点名拒收，而不是静默丢弃或默默降级。
func TestObserverCannotControl(t *testing.T) {
	t.Run("不得声明运行阶段", func(t *testing.T) {
		obs := baseObs()
		obs.Kind = "turn.finished" // 用观测类 kind 但夹带阶段，先撞载荷错位
		mustReject(t, newIng().Ingest(observedSrc(), []Observation{obs}), ReasonPayloadKindMismatch)

		obs = baseObs() // 直接用 run.phase：控制类事实
		rej := mustReject(t, newIng().Ingest(observedSrc(), []Observation{obs}), ReasonAuthorityEscalation)
		if rej.Field != "phase" {
			t.Fatalf("应点名 phase，实际 %s", rej.Field)
		}
	})

	t.Run("不得把任务置为 done", func(t *testing.T) {
		obs := baseObs()
		obs.Kind = KindTaskStatus
		obs.Phase = nil
		obs.TaskStatus = statusOf(model.TaskDone)

		rej := mustReject(t, newIng().Ingest(observedSrc(), []Observation{obs}), ReasonAuthorityEscalation)
		if rej.Field != "task_status" {
			t.Fatalf("应点名 task_status，实际 %s", rej.Field)
		}
		if rej.Reason.Class() != model.ClassTerminal {
			t.Fatalf("越权应判终止，实际 %s", rej.Reason.Class())
		}
	})

	t.Run("不得取得任务所有权", func(t *testing.T) {
		obs := baseObs()
		obs.Kind = KindClaimRequested
		obs.Phase = nil
		obs.Claim = &Claim{Instance: "ti_vn802a", Agent: "cursor-goalagents-1"}

		rej := mustReject(t, newIng().Ingest(observedSrc(), []Observation{obs}), ReasonAuthorityEscalation)
		if rej.Field != "claim" {
			t.Fatalf("应点名 claim，实际 %s", rej.Field)
		}
	})

	t.Run("控制类 kind 不得替观测域开洞", func(t *testing.T) {
		obs := baseObs()
		obs.Kind = KindLeaseReleased
		obs.Phase = nil
		obs.RunID = ""
		obs.Generation = nil

		rej := mustReject(t, newIng().Ingest(observedSrc(), []Observation{obs}), ReasonAuthorityEscalation)
		if rej.Field != "kind" {
			t.Fatalf("应点名 kind，实际 %s", rej.Field)
		}
	})
}

// TestObserverReportStaysUnattributed 是「不把旁路说法当成归属」的核心：
// 旁路提到某个 Run 时，事件本身仍是未归属，声明值只留在载荷里可查。
func TestObserverReportStaysUnattributed(t *testing.T) {
	g := newIng()
	obs := Observation{
		RecordRef:     "obs://sideband/2026-10-23/0001",
		RecordType:    "sideband.note",
		SchemaVersion: 1,
		SourceKey:     "note-0001",
		Kind:          "turn.finished",
		OccurredAt:    at(10),
		ReceivedAt:    *at(12),
		Instance:      "ti_vn802a", // 它说它看到的是这道题
		RunID:         "run_0001",
		Generation:    gen(2),
		Payload:       map[string]any{"note": "看到进程退出"},
	}

	rec := mustAccept(t, g.Ingest(observedSrc(), []Observation{obs}))
	if rec.Attribution != Unattributed {
		t.Fatalf("旁路记录必须未归属，实际 %s", rec.Attribution)
	}
	if rec.Event.RunID != "" {
		t.Fatalf("未归属事件不得带 run_id，实际 %q", rec.Event.RunID)
	}
	if rec.Reported.Instance != "ti_vn802a" || rec.Reported.RunID != "run_0001" ||
		rec.Reported.Generation == nil || *rec.Reported.Generation != 2 {
		t.Fatalf("来源声明必须原样保留：%+v", rec.Reported)
	}

	body, err := rec.Payload()
	if err != nil {
		t.Fatalf("生成载荷失败：%v", err)
	}
	var decoded struct {
		Attribution string `json:"attribution"`
		Reported    struct {
			Instance string `json:"task_instance"`
			RunID    string `json:"run_id"`
		} `json:"reported"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("载荷解析失败：%v", err)
	}
	if decoded.Attribution != string(Unattributed) {
		t.Fatalf("载荷里的归属应为 unattributed，实际 %q", decoded.Attribution)
	}
	if decoded.Reported.Instance != "ti_vn802a" || decoded.Reported.RunID != "run_0001" {
		t.Fatalf("载荷里应保留来源声明：%+v", decoded.Reported)
	}
}

// TestClaimAgentMismatchRejected 覆盖「替别人领取」：受管来源也不能冒充他人身份。
func TestClaimAgentMismatchRejected(t *testing.T) {
	src := managedSrc()
	src.Agent = "workbuddy-goalagents-2"

	obs := baseObs()
	obs.Kind = KindClaimRequested
	obs.Phase = nil
	obs.Claim = &Claim{Instance: "ti_vn802a", Agent: "cursor-goalagents-1"}

	rej := mustReject(t, newIng().Ingest(src, []Observation{obs}), ReasonClaimAgentMismatch)
	if rej.Field != "claim.agent" {
		t.Fatalf("应点名 claim.agent，实际 %s", rej.Field)
	}
}

// TestManagedClaimIsAttributed 覆盖正例：受管来源声明自己领取，事件可归属。
func TestManagedClaimIsAttributed(t *testing.T) {
	g := newIng()
	obs := baseObs()
	obs.Kind = KindClaimRequested
	obs.Phase = nil
	obs.Claim = &Claim{Instance: "ti_vn802a", Agent: "cursor-goalagents-1"}

	rec := mustAccept(t, g.Ingest(managedSrc(), []Observation{obs}))
	if rec.Attribution != Attributed {
		t.Fatalf("受管来源的合法领取应可归属，实际 %s", rec.Attribution)
	}
	// 领取声明只是一条事件：它没有在这里改任何任务状态。
	if rec.Event.Kind != KindClaimRequested {
		t.Fatalf("kind 应为 %s，实际 %s", KindClaimRequested, rec.Event.Kind)
	}
}

// TestUnpairedRunIdentityRejected 覆盖「换会话重建」的常见错误：
// 只给 run_id 不给代际，无法判定是否旧代际写入，必须拒收而不是猜成第 1 代。
func TestUnpairedRunIdentityRejected(t *testing.T) {
	obs := baseObs()
	obs.Generation = nil
	mustReject(t, newIng().Ingest(managedSrc(), []Observation{obs}), ReasonRunGenerationUnpaired)

	obs = baseObs()
	obs.RunID = ""
	mustReject(t, newIng().Ingest(managedSrc(), []Observation{obs}), ReasonRunGenerationUnpaired)
}

// TestLegacyDecodeIsUnattributedAndEstimated 覆盖旧字段归一：
// 旧 ts 是入库时刻，因此质量只能 estimated；旧 task_id 是显示编号，不能猜成实例。
func TestLegacyDecodeIsUnattributedAndEstimated(t *testing.T) {
	raw := []byte(`{"ts":"2026-10-20T08:00:00Z","actor":"cursor-goalagents-1","type":"progress","task_id":"VN-802","data":{"note":"开始读契约"}}`)

	obs, err := DecodeLegacy("events:4711", raw)
	if err != nil {
		t.Fatalf("旧记录应可归一：%v", err)
	}
	if !obs.Legacy || !obs.Backfill {
		t.Fatalf("旧记录必须标记 legacy 与 backfill：%+v", obs)
	}
	if obs.Kind != "legacy.progress" {
		t.Fatalf("裸词类型应加命名空间，实际 %q", obs.Kind)
	}
	if obs.Quality != model.QualityEstimated {
		t.Fatalf("旧 ts 不可信，质量应为 estimated，实际 %s", obs.Quality)
	}
	if obs.Instance != "" {
		t.Fatalf("旧记录不得凭 display_id 猜实例，实际 %q", obs.Instance)
	}
	if obs.SourceKey != "events:4711" {
		t.Fatalf("旧表主键应作为稳定键，实际 %q", obs.SourceKey)
	}
	if obs.Payload["legacy_task_id"] != "VN-802" || obs.Payload["legacy_actor"] != "cursor-goalagents-1" {
		t.Fatalf("旧字段必须原样留在载荷里：%+v", obs.Payload)
	}

	g := newIng()
	rec := mustAccept(t, g.Ingest(LegacySource(), []Observation{obs}))
	if rec.Attribution != Unattributed {
		t.Fatalf("旧记录只能未归属，实际 %s", rec.Attribution)
	}
	if rec.Event.Quality != model.QualityEstimated {
		t.Fatalf("旧记录质量必须停在 estimated，实际 %s", rec.Event.Quality)
	}
	if !rec.HasNote(NoteLegacyTimestamp) {
		t.Fatalf("必须说明时间为何降级：%v", rec.QualityNotes)
	}
	legacyTS := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	if !rec.Event.OccurredAt.Equal(legacyTS) {
		t.Fatalf("发生时间应取自旧 ts，实际 %s", rec.Event.OccurredAt)
	}
}

// TestLegacyFieldsAreNotAssumedControl 覆盖「旧记录不得冒充控制事实」：
// 旧表的 type 若恰好是运行阶段名，加命名空间后也不是控制类 kind。
func TestLegacyFieldsAreNotAssumedControl(t *testing.T) {
	for _, typ := range []string{"run.phase", "task.status", "lease.released"} {
		raw := []byte(`{"ts":"2026-10-20T08:00:00Z","type":"` + typ + `","data":{}}`)
		obs, err := DecodeLegacy("events:1", raw)
		if err != nil {
			t.Fatalf("%s 归一失败：%v", typ, err)
		}
		if controlKinds[obs.Kind] {
			t.Fatalf("旧记录不得恢复成控制类 kind：%s", obs.Kind)
		}
		res := newIng().Ingest(LegacySource(), []Observation{obs})
		if len(res.Rejected) != 0 {
			t.Fatalf("%s 应作为观测事实接受，实际拒收 %+v", typ, res.Rejected)
		}
	}
}

// TestLegacyDecodeRejectsUnusableRows 覆盖旧数据的两种坏行：缺 ts 与 ts 不可解析。
// 两种都不得被补成「现在」，只能如实报错。
func TestLegacyDecodeRejectsUnusableRows(t *testing.T) {
	cases := map[string]string{
		"缺 ts":    `{"type":"progress"}`,
		"ts 不可解析": `{"ts":"昨天下午","type":"progress"}`,
		"缺 type":  `{"ts":"2026-10-20T08:00:00Z"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeLegacy("events:1", []byte(raw))
			if err == nil {
				t.Fatal("坏行必须报错，不得补造字段")
			}
			if _, ok := model.AsError(err); !ok {
				t.Fatalf("应为类型化错误，实际 %T", err)
			}
		})
	}
}
