package events

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// TestIngestManagedRunPhase 覆盖最基本的正例：受管来源写一条运行阶段事件，
// 双时间戳、身份、归属与游标都要落到事件上。
func TestIngestManagedRunPhase(t *testing.T) {
	g := newIng()
	rec := mustAccept(t, g.Ingest(managedSrc(), []Observation{baseObs()}))

	ev := rec.Event
	if ev.Kind != KindRunPhase {
		t.Fatalf("kind 应为 %s，实际 %s", KindRunPhase, ev.Kind)
	}
	if !ev.EventID.Valid() {
		t.Fatalf("事件身份非法：%q", ev.EventID)
	}
	if ev.Cursor != 1 {
		t.Fatalf("首条事件游标应为 1，实际 %d", ev.Cursor)
	}
	if ev.SchemaVersion != 1 {
		t.Fatalf("schema 版本应随来源，实际 %d", ev.SchemaVersion)
	}
	if !ev.OccurredAt.Equal(*at(10)) || !ev.ReceivedAt.Equal(*at(11)) {
		t.Fatalf("双时间戳被改写：occurred=%s received=%s", ev.OccurredAt, ev.ReceivedAt)
	}
	if ev.RunID != "run_0001" || ev.Phase == nil || *ev.Phase != model.PhaseRunning {
		t.Fatalf("运行归属丢失：run=%q phase=%v", ev.RunID, ev.Phase)
	}
	if ev.Quality != model.QualityObserved {
		t.Fatalf("同批到达应为 observed，实际 %s", ev.Quality)
	}
	if rec.Attribution != Attributed || rec.LateArrival || rec.Backfill || rec.Legacy {
		t.Fatalf("注解不符：%+v", rec)
	}
	if rec.Key != "msg-0001" || rec.KeyDerived {
		t.Fatalf("来源键应被采用且非合成：key=%q derived=%v", rec.Key, rec.KeyDerived)
	}
	if len(rec.Fingerprint) != 64 {
		t.Fatalf("指纹应为 64 位十六进制，实际 %q", rec.Fingerprint)
	}
	if _, ok := g.Frontier(managedSrc()); !ok {
		t.Fatal("接受之后必须登记发生时间前沿")
	}
}

// TestReceivedAtDefaultsToNow 覆盖来源没给接收时间：接收时间由摄取方现取，
// 而不是把发生时间抄一遍。
func TestReceivedAtDefaultsToNow(t *testing.T) {
	g := New(Options{Now: func() time.Time { return t0.Add(90 * time.Minute) }})
	obs := baseObs()
	obs.ReceivedAt = time.Time{}

	rec := mustAccept(t, g.Ingest(managedSrc(), []Observation{obs}))
	if !rec.Event.ReceivedAt.Equal(t0.Add(90 * time.Minute)) {
		t.Fatalf("接收时间应为摄取时刻，实际 %s", rec.Event.ReceivedAt)
	}
	if rec.Event.OccurredAt.Equal(rec.Event.ReceivedAt) {
		t.Fatal("发生时间不得被接收时间顶替")
	}
}

// TestClockSkewDowngradesQuality 覆盖「发生晚于接收」：来源时钟不可信，
// 只能记 estimated，并且必须留下降级说明（契约 E262 的同一判据）。
func TestClockSkewDowngradesQuality(t *testing.T) {
	g := newIng()
	obs := baseObs()
	obs.OccurredAt = at(20)
	obs.ReceivedAt = *at(11)

	rec := mustAccept(t, g.Ingest(managedSrc(), []Observation{obs}))
	if rec.Event.Quality != model.QualityEstimated {
		t.Fatalf("时钟倒挂应降级为 estimated，实际 %s", rec.Event.Quality)
	}
	if !rec.HasNote(NoteClockSkew) {
		t.Fatalf("必须记录降级原因，实际 %v", rec.QualityNotes)
	}
	// 原值不丢：发生时间照旧保留，只是质量降级。
	if !rec.Event.OccurredAt.Equal(*at(20)) {
		t.Fatalf("发生时间被改写：%s", rec.Event.OccurredAt)
	}
}

// TestRejections 逐条覆盖每个拒收原因：既验证原因码，也验证「拒收不占游标、
// 不推进前沿」——否则一次脏数据会永久抬高水位。
func TestRejections(t *testing.T) {
	cases := []struct {
		name   string
		src    Source
		mutate func(*Observation)
		want   Reason
		field  string
	}{
		{
			name: "来源未绑定",
			src:  Source{Domain: DomainManaged, Adapter: ""},
			want: ReasonSourceUnbound, field: "source",
		},
		{
			name:   "缺日志位置",
			src:    managedSrc(),
			mutate: func(o *Observation) { o.RecordRef = "" },
			want:   ReasonRecordRefMissing, field: "record_ref",
		},
		{
			name:   "缺记录类型",
			src:    managedSrc(),
			mutate: func(o *Observation) { o.RecordType = "" },
			want:   ReasonRecordTypeMissing, field: "record_type",
		},
		{
			name:   "缺 schema 版本",
			src:    managedSrc(),
			mutate: func(o *Observation) { o.SchemaVersion = 0 },
			want:   ReasonSchemaUnversioned, field: "schema_version",
		},
		{
			name:   "缺 kind",
			src:    managedSrc(),
			mutate: func(o *Observation) { o.Kind = "" },
			want:   ReasonKindMissing, field: "kind",
		},
		{
			name:   "kind 裸词",
			src:    managedSrc(),
			mutate: func(o *Observation) { o.Kind = "progress" },
			want:   ReasonKindMalformed, field: "kind",
		},
		{
			name:   "缺发生时间",
			src:    managedSrc(),
			mutate: func(o *Observation) { o.OccurredAt = nil },
			want:   ReasonOccurredAtMissing, field: "occurred_at",
		},
		{
			name:   "run 与 gen 未成对",
			src:    managedSrc(),
			mutate: func(o *Observation) { o.Generation = nil },
			want:   ReasonRunGenerationUnpaired, field: "run_id",
		},
		{
			name:   "质量取值非法",
			src:    managedSrc(),
			mutate: func(o *Observation) { o.Quality = "guess" },
			want:   ReasonQualityInvalid, field: "quality",
		},
		{
			name:   "未知阶段",
			src:    managedSrc(),
			mutate: func(o *Observation) { o.Phase = phaseOf("busy") },
			want:   ReasonPhaseUnknown, field: "phase",
		},
		{
			name: "未知任务状态",
			src:  managedSrc(),
			mutate: func(o *Observation) {
				o.Kind = KindTaskStatus
				o.Phase = nil
				o.TaskStatus = statusOf("finished")
			},
			want: ReasonTaskStatusUnknown, field: "task_status",
		},
		{
			name: "领取声明缺实例",
			src:  managedSrc(),
			mutate: func(o *Observation) {
				o.Kind = KindClaimRequested
				o.Phase = nil
				o.Claim = &Claim{Agent: "cursor-goalagents-1"}
			},
			want: ReasonClaimUnbound, field: "claim.task_instance",
		},
		{
			name:   "阶段夹带在别的 kind 里",
			src:    managedSrc(),
			mutate: func(o *Observation) { o.Kind = "turn.finished" },
			want:   ReasonPayloadKindMismatch, field: "phase",
		},
		{
			name: "run.phase 缺阶段值",
			src:  managedSrc(),
			mutate: func(o *Observation) {
				o.Phase = nil
			},
			want: ReasonPayloadKindMismatch, field: "phase",
		},
		{
			name: "task.status 缺状态值",
			src:  managedSrc(),
			mutate: func(o *Observation) {
				o.Kind = KindTaskStatus
				o.Phase = nil
			},
			want: ReasonPayloadKindMismatch, field: "task_status",
		},
		{
			name: "claim.requested 缺声明",
			src:  managedSrc(),
			mutate: func(o *Observation) {
				o.Kind = KindClaimRequested
				o.Phase = nil
			},
			want: ReasonPayloadKindMismatch, field: "claim",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			obs := baseObs()
			if c.mutate != nil {
				c.mutate(&obs)
			}
			g := newIng()
			rejection := mustReject(t, g.Ingest(c.src, []Observation{obs}), c.want)
			if rejection.Field != c.field {
				t.Fatalf("期望定位到 %s，实际 %s", c.field, rejection.Field)
			}
			if g.LastCursor() != 0 {
				t.Fatalf("拒收不得占用游标，实际推进到 %d", g.LastCursor())
			}
			if _, ok := g.Frontier(c.src); ok {
				t.Fatal("拒收不得登记发生时间前沿")
			}
			if rejection.Err() == nil {
				t.Fatal("拒收必须能映射成带分类的错误")
			}
		})
	}
}

// TestRejectionErrorCarriesPortClass 确认拒收错误走的是已登记的端口错误码，
// 分类（retryable/conflict/terminal/unknown）不由本包自创。
func TestRejectionErrorCarriesPortClass(t *testing.T) {
	g := newIng()
	obs := baseObs()
	obs.RecordRef = ""

	res := g.Ingest(managedSrc(), []Observation{obs})
	if len(res.Rejected) != 1 {
		t.Fatalf("期望 1 条拒收，实际 %d", len(res.Rejected))
	}
	err := res.Rejected[0].Err()
	te, ok := model.AsError(err)
	if !ok {
		t.Fatalf("应为类型化错误，实际 %T", err)
	}
	if !te.Port.Known() {
		t.Fatalf("端口错误码未登记：%s", te.Port)
	}
	if te.Class != model.ClassUnknown {
		t.Fatalf("缺日志位置应判为证据不足（unknown），实际 %s", te.Class)
	}
	if !errors.Is(err, model.PortFailure(model.PortErrUnparseableRecord, "")) {
		t.Fatalf("应可按端口码匹配，实际 %v", err)
	}
}

// TestAllReasonsRegistered 保证原因码表本身没有半登记项：
// 每个码都有中文说明、都映射到已登记的端口码。
func TestAllReasonsRegistered(t *testing.T) {
	all := AllReasons()
	if len(all) == 0 {
		t.Fatal("原因码表为空")
	}
	for _, r := range all {
		if !r.Known() {
			t.Fatalf("%s 未登记", r)
		}
		if r.Title() == "" {
			t.Fatalf("%s 缺中文说明", r)
		}
		if !r.PortError().Known() {
			t.Fatalf("%s 映射到未登记的端口码 %s", r, r.PortError())
		}
		if !r.Class().Valid() {
			t.Fatalf("%s 的分类非法：%s", r, r.Class())
		}
	}
	// 反例：空白原因码不得被当成「已登记」。
	if Reason("").Known() {
		t.Fatal("空原因码不应被登记")
	}
}

// TestPayloadRoundTrip 固定入库载荷的形状：重启后要靠它恢复去重索引，
// 所以 source_key、fingerprint、双时间戳与迟到标记都必须在里面。
func TestPayloadRoundTrip(t *testing.T) {
	g := newIng()
	rec := mustAccept(t, g.Ingest(managedSrc(), []Observation{baseObs()}))

	body, err := rec.Payload()
	if err != nil {
		t.Fatalf("生成载荷失败：%v", err)
	}
	var decoded struct {
		Source struct {
			Domain  string `json:"domain"`
			Adapter string `json:"adapter"`
			Agent   string `json:"agent"`
		} `json:"source"`
		Record struct {
			Ref           string `json:"ref"`
			Type          string `json:"type"`
			SchemaVersion int    `json:"schema_version"`
		} `json:"record"`
		Ingest struct {
			SourceKey   string `json:"source_key"`
			Fingerprint string `json:"fingerprint"`
			Legacy      bool   `json:"legacy"`
		} `json:"ingest"`
		Attribution string `json:"attribution"`
		Time        struct {
			OccurredAt  time.Time `json:"occurred_at"`
			ReceivedAt  time.Time `json:"received_at"`
			LateArrival bool      `json:"late_arrival"`
			Backfill    bool      `json:"backfill"`
		} `json:"time"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("载荷不是合法 JSON：%v", err)
	}
	if decoded.Source.Domain != "managed" || decoded.Source.Adapter != "cursor-cli" ||
		decoded.Source.Agent != "cursor-goalagents-1" {
		t.Fatalf("来源绑定丢失：%+v", decoded.Source)
	}
	if decoded.Record.Ref != "log://cursor/2026-10-23/0001" || decoded.Record.Type != "stream.session" {
		t.Fatalf("记录位置丢失：%+v", decoded.Record)
	}
	if decoded.Ingest.SourceKey != "msg-0001" || decoded.Ingest.Fingerprint != rec.Fingerprint {
		t.Fatalf("重建索引所需的键与指纹丢失：%+v", decoded.Ingest)
	}
	if decoded.Attribution != string(Attributed) {
		t.Fatalf("归属结论丢失：%q", decoded.Attribution)
	}
	if !decoded.Time.OccurredAt.Equal(*at(10)) || !decoded.Time.ReceivedAt.Equal(*at(11)) {
		t.Fatalf("载荷里的双时间戳不符：%+v", decoded.Time)
	}

	// 同一 Record 反复编码必须字节一致（map 键有序）。
	again, err := rec.Payload()
	if err != nil {
		t.Fatalf("重复编码失败：%v", err)
	}
	if string(body) != string(again) {
		t.Fatal("载荷编码不稳定，无法用于比对")
	}
}
