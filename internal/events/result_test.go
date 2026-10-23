package events

import (
	"strings"
	"testing"
	"time"
)

// TestResultAccessors 覆盖 Result 的两个便捷出口：落库方要事件列表，
// 调用方要先判断「这批到底有没有事」。
func TestResultAccessors(t *testing.T) {
	var zero Result
	if !zero.Empty() {
		t.Fatal("空结果应报告 Empty")
	}
	if evs := zero.Events(); len(evs) != 0 {
		t.Fatalf("空结果不应有事件，实际 %d", len(evs))
	}

	res := newIng().Ingest(managedSrc(), []Observation{baseObs()})
	if res.Empty() {
		t.Fatal("有接受的批次不应报告 Empty")
	}
	evs := res.Events()
	if len(evs) != 1 || evs[0].EventID != res.Accepted[0].Event.EventID {
		t.Fatalf("Events() 应与 Accepted 一一对应：%+v", evs)
	}
}

// TestRejectionErrorText 覆盖拒收的可读文案：带日志位置时能直接定位到源记录，
// 不带时也必须能读。
func TestRejectionErrorText(t *testing.T) {
	withRef := Rejection{RecordRef: "log://x/1", Reason: ReasonKindMalformed}
	if got := withRef.Error(); !strings.Contains(got, "log://x/1") || !strings.Contains(got, string(ReasonKindMalformed)) {
		t.Fatalf("文案应含位置与原因：%s", got)
	}
	noRef := Rejection{Reason: ReasonKeyConflict}
	if got := noRef.Error(); !strings.Contains(got, string(ReasonKeyConflict)) {
		t.Fatalf("无位置时仍要给出原因：%s", got)
	}
	if got := noRef.Err(); got == nil || !strings.Contains(got.Error(), noRef.Reason.Title()) {
		t.Fatalf("Err() 应带中文说明，实际 %v", got)
	}
	// 空原因不产生错误：调用方不该拿到一个「看起来失败」的 nil 语义错误。
	if err := (Rejection{}).Err(); err != nil {
		t.Fatalf("空原因不应产生错误：%v", err)
	}
}

// TestSourceValidateReportsEachField 覆盖来源绑定校验的逐字段定位：
// 上层要能告诉接入方「到底哪一项没填」。
func TestSourceValidateReportsEachField(t *testing.T) {
	cases := []struct {
		name  string
		src   Source
		field string
	}{
		{"域未知", Source{Adapter: "cursor-cli"}, "source.domain"},
		{"接入器缺名", Source{Domain: DomainManaged}, "source.adapter"},
		{"上报者非法", Source{Domain: DomainManaged, Adapter: "a", Agent: "Cursor CLI"}, "source.agent"},
		{"节点非法", Source{Domain: DomainManaged, Adapter: "a", NodeID: "Node 1"}, "source.node_id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := c.src.Validate()
			if len(errs) != 1 {
				t.Fatalf("期望 1 条违反，实际 %d（%v）", len(errs), errs)
			}
			if errs[0].Path != c.field {
				t.Fatalf("应定位到 %s，实际 %s", c.field, errs[0].Path)
			}
		})
	}

	ok := Source{Domain: DomainObserved, Adapter: "cursor-cli", NodeID: "mac-mini", Agent: "cursor-goalagents-1"}
	if errs := ok.Validate(); len(errs) != 0 {
		t.Fatalf("合法来源不应报错：%v", errs)
	}
}

// TestAttributionClosedSet 覆盖归属结论的封闭集合：多一个值就意味着上层有分支没写。
func TestAttributionClosedSet(t *testing.T) {
	if !Attributed.Valid() || !Unattributed.Valid() {
		t.Fatal("已定义的两个归属取值必须合法")
	}
	if Attribution("guessed").Valid() {
		t.Fatal("未定义归属不得通过校验")
	}
}

// TestNewDefaultsToWallClock 覆盖不加 Now 选项时接收时间取真实时钟：
// 生产路径不会忘了传回调就变成零时间。
func TestNewDefaultsToWallClock(t *testing.T) {
	before := time.Now().UTC().Add(-time.Minute)
	g := New(Options{})
	obs := baseObs()
	obs.ReceivedAt = time.Time{}
	obs.OccurredAt = at(10)

	rec := mustAccept(t, g.Ingest(managedSrc(), []Observation{obs}))
	if rec.Event.ReceivedAt.Before(before) {
		t.Fatalf("接收时间应取当前时钟，实际 %s", rec.Event.ReceivedAt)
	}
}
