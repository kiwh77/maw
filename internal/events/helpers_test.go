package events

import (
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// t0 是所有测试的时间原点：固定值让指纹、迟到判定与水位断言都可复现。
var t0 = time.Date(2026, 10, 23, 2, 0, 0, 0, time.UTC)

// at 返回 t0 之后第 m 分钟的时间指针。
func at(m int) *time.Time {
	t := t0.Add(time.Duration(m) * time.Minute)
	return &t
}

// src 构造受管来源。
func managedSrc() Source {
	return Source{Domain: DomainManaged, Adapter: "cursor-cli", Agent: "cursor-goalagents-1"}
}

// observedSrc 构造旁路来源（无登录身份）。
func observedSrc() Source {
	return Source{Domain: DomainObserved, Adapter: "cursor-cli"}
}

// gen 返回代际指针。
func gen(v int) *model.Generation {
	g := model.Generation(v)
	return &g
}

// phaseOf 返回运行阶段指针。
func phaseOf(p model.RunPhase) *model.RunPhase { return &p }

// statusOf 返回任务状态指针。
func statusOf(s model.TaskStatus) *model.TaskStatus { return &s }

// baseObs 是一条合法的受管运行阶段观测；各用例只改需要的字段。
func baseObs() Observation {
	return Observation{
		RecordRef:     "log://cursor/2026-10-23/0001",
		RecordType:    "stream.session",
		SchemaVersion: 1,
		SourceKey:     "msg-0001",
		Kind:          KindRunPhase,
		OccurredAt:    at(10),
		ReceivedAt:    *at(11),
		Instance:      "ti_vn802a",
		RunID:         "run_0001",
		Generation:    gen(1),
		Phase:         phaseOf(model.PhaseRunning),
		Payload:       map[string]any{"line": 12},
	}
}

// newIng 构造一个时间固定的摄取器，避免 ReceivedAt 缺省值时依赖真实时钟。
func newIng() *Ingestor {
	return New(Options{Now: func() time.Time { return t0.Add(60 * time.Minute) }})
}

// mustReject 断言批次只产生一条指定原因的拒收。
func mustReject(t *testing.T, res Result, want Reason) Rejection {
	t.Helper()
	if len(res.Accepted) != 0 {
		t.Fatalf("不应产生事件，却接受了 %d 条", len(res.Accepted))
	}
	if len(res.Rejected) != 1 {
		t.Fatalf("期望 1 条拒收，实际 %d 条（%+v）", len(res.Rejected), res.Rejected)
	}
	if got := res.Rejected[0].Reason; got != want {
		t.Fatalf("期望原因 %s，实际 %s（%s）", want, got, res.Rejected[0].Msg)
	}
	return res.Rejected[0]
}

// mustAccept 断言批次恰好接受一条，并返回它。
func mustAccept(t *testing.T, res Result) Record {
	t.Helper()
	if len(res.Rejected) != 0 {
		t.Fatalf("不应拒收：%+v", res.Rejected[0])
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("不应冲突：%+v", res.Conflicts[0])
	}
	if len(res.Accepted) != 1 {
		t.Fatalf("期望接受 1 条，实际 %d 条", len(res.Accepted))
	}
	return res.Accepted[0]
}
