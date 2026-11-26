package events

import (
	"strings"
	"testing"
)

// TestReplayIsIdempotent 覆盖幂等重放：同域同键同内容再来一次，不新建事件，
// 返回的仍是第一次那个事件身份。
func TestReplayIsIdempotent(t *testing.T) {
	g := newIng()
	obs := baseObs()

	first := mustAccept(t, g.Ingest(managedSrc(), []Observation{obs}))
	res := g.Ingest(managedSrc(), []Observation{obs})

	if len(res.Accepted) != 0 {
		t.Fatalf("重放不得新建事件，实际接受 %d 条", len(res.Accepted))
	}
	if len(res.Duplicates) != 1 {
		t.Fatalf("期望 1 条重放，实际 %d", len(res.Duplicates))
	}
	if res.Duplicates[0].EventID != first.Event.EventID {
		t.Fatalf("重放应复用原事件身份：%s ≠ %s", res.Duplicates[0].EventID, first.Event.EventID)
	}
	if g.LastCursor() != first.Event.Cursor {
		t.Fatalf("重放不得推进游标：%d", g.LastCursor())
	}

	// 接收时间变了仍是重放（指纹刻意不含 received_at）。
	obs.ReceivedAt = *at(30)
	res = g.Ingest(managedSrc(), []Observation{obs})
	if len(res.Duplicates) != 1 {
		t.Fatalf("换接收时间的重读仍是重放，实际 %+v", res)
	}
}

// TestSameKeyDifferentContentConflicts 覆盖同键不同内容：身份被两种说法争用，
// 既不覆盖旧事件，也不新建，必须如实报冲突让人判断。
func TestSameKeyDifferentContentConflicts(t *testing.T) {
	g := newIng()
	first := mustAccept(t, g.Ingest(managedSrc(), []Observation{baseObs()}))

	other := baseObs()
	other.Phase = phaseOf("completed") // 同一条日志位置，却说了另一种阶段

	res := g.Ingest(managedSrc(), []Observation{other})
	if len(res.Accepted) != 0 {
		t.Fatal("同键不同内容不得新建事件")
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("期望 1 条冲突，实际 %+v", res)
	}
	c := res.Conflicts[0]
	if c.Existing != first.Event.EventID {
		t.Fatalf("冲突应指向已入库事件：%s", c.Existing)
	}
	if c.Reason != ReasonKeyConflict {
		t.Fatalf("冲突原因应为 %s，实际 %s", ReasonKeyConflict, c.Reason)
	}
	if c.Reason.Class() != "conflict" {
		t.Fatalf("同键争用应归 conflict 类，实际 %s", c.Reason.Class())
	}
}

// TestIdenticalContentDifferentOccurrenceIsIndependent 是本卡第一条验收的核心：
// 去重按来源身份而不是内容。两次真的发生（内容一字不差、时间不同）必须留两条事件，
// 否则会把「重复劳动」洗掉，统计就少了一半工作量。
func TestIdenticalContentDifferentOccurrenceIsIndependent(t *testing.T) {
	g := newIng()
	a := baseObs()
	a.SourceKey = "" // 来源没给稳定键

	b := baseObs()
	b.SourceKey = ""
	b.OccurredAt = at(40) // 同样的内容，半小时后又发生一次

	res := g.Ingest(managedSrc(), []Observation{a, b})
	if len(res.Accepted) != 2 {
		t.Fatalf("两次独立发生必须留两条事件，实际接受 %d 条（重放 %d / 冲突 %d / 拒收 %d）",
			len(res.Accepted), len(res.Duplicates), len(res.Conflicts), len(res.Rejected))
	}
	if res.Accepted[0].Event.EventID == res.Accepted[1].Event.EventID {
		t.Fatal("两条独立事件不得共用身份")
	}
	if res.Accepted[0].Event.Cursor >= res.Accepted[1].Event.Cursor {
		t.Fatal("游标必须按接收顺序严格递增")
	}
	for _, rec := range res.Accepted {
		if !rec.KeyDerived || !strings.HasPrefix(rec.Key, "derived:") {
			t.Fatalf("缺来源键时应合成键并标注：key=%q derived=%v", rec.Key, rec.KeyDerived)
		}
	}

	// 同内容同时间再来一次，才是重放。
	res = g.Ingest(managedSrc(), []Observation{a})
	if len(res.Duplicates) != 1 {
		t.Fatalf("同内容同发生时间应判重放，实际 %+v", res)
	}
}

// TestSameKeyAcrossDomainsIsIndependent 覆盖分域：接入器不同就是不同来源，
// 即便键与内容都相同，也是两条记录（「谁说的」本身是事实）。
func TestSameKeyAcrossDomainsIsIndependent(t *testing.T) {
	g := newIng()

	managed := baseObs()
	// 旁路只能报观测类事实：同一个键、同样的发生时间与载荷，但内容属于 turn。
	sideband := Observation{
		RecordRef:     "log://cursor/2026-10-23/0001",
		RecordType:    "stream.session",
		SchemaVersion: 1,
		SourceKey:     "msg-0001",
		Kind:          "turn.finished",
		OccurredAt:    at(10),
		ReceivedAt:    *at(11),
		Payload:       map[string]any{"line": 12},
	}

	res := g.Ingest(managedSrc(), []Observation{managed})
	if len(res.Accepted) != 1 {
		t.Fatalf("首次应接受，实际 %+v", res)
	}
	// 同一接入器的观测域：域不同 → 不同去重域。
	res = g.Ingest(Source{Domain: DomainObserved, Adapter: "cursor-cli"}, []Observation{sideband})
	if len(res.Accepted) != 1 {
		t.Fatalf("跨域同键应各自独立，实际 重放 %d / 冲突 %d / 拒收 %+v",
			len(res.Duplicates), len(res.Conflicts), res.Rejected)
	}

	// 同域不同接入器 → 也不同去重域。
	other := baseObs()
	other.RunID = "run_0002"
	other.Generation = gen(1)
	res = g.Ingest(Source{Domain: DomainManaged, Adapter: "trae-cli"}, []Observation{other})
	if len(res.Accepted) != 1 {
		t.Fatalf("换接入器应视为另一来源，实际 %+v", res)
	}
}

// TestEventIDIsDeterministic 覆盖确定性身份：两个互不相干的进程处理同一条来源记录，
// 得到的身份必须一致，否则重启后无法靠 id 判重。
func TestEventIDIsDeterministic(t *testing.T) {
	a := mustAccept(t, newIng().Ingest(managedSrc(), []Observation{baseObs()}))
	b := mustAccept(t, newIng().Ingest(managedSrc(), []Observation{baseObs()}))
	if a.Event.EventID != b.Event.EventID {
		t.Fatalf("同来源同键应得同一身份：%s ≠ %s", a.Event.EventID, b.Event.EventID)
	}
	if !strings.HasPrefix(string(a.Event.EventID), "evt_") {
		t.Fatalf("事件身份形态不符：%s", a.Event.EventID)
	}
}

// TestSeedRestoresIdempotency 覆盖进程重启：用落库载荷里的键与指纹重建索引后，
// 重放整批不得多出事件。
func TestSeedRestoresIdempotency(t *testing.T) {
	src := managedSrc()
	batch := []Observation{baseObs()}
	second := baseObs()
	second.SourceKey = "msg-0002"
	second.OccurredAt = at(12)
	second.ReceivedAt = *at(13)
	second.Phase = phaseOf("completed")
	batch = append(batch, second)

	first := newIng()
	stored := first.Ingest(src, batch).Accepted
	if len(stored) != 2 {
		t.Fatalf("准备数据失败：接受 %d 条", len(stored))
	}

	// 模拟重启：新摄取器 + 用已落库记录重建索引。
	restarted := newIng()
	if rej := restarted.Seed(src, stored); len(rej) != 0 {
		t.Fatalf("重建索引不应有拒收：%+v", rej)
	}
	res := restarted.Ingest(src, batch)
	if len(res.Accepted) != 0 {
		t.Fatalf("重启后重放不得新建事件，实际 %d 条", len(res.Accepted))
	}
	if len(res.Duplicates) != 2 {
		t.Fatalf("两条都应判重放，实际 %d", len(res.Duplicates))
	}
	if restarted.LastCursor() != first.LastCursor() {
		t.Fatalf("重建后游标水位应延续：%d ≠ %d", restarted.LastCursor(), first.LastCursor())
	}

	// 重启后接受的新事件，游标必须继续增长而不是回到 1。
	fresh := baseObs()
	fresh.SourceKey = "msg-0003"
	fresh.OccurredAt = at(20)
	fresh.ReceivedAt = *at(21)
	rec := mustAccept(t, restarted.Ingest(src, []Observation{fresh}))
	if rec.Event.Cursor <= stored[len(stored)-1].Event.Cursor {
		t.Fatalf("新事件游标必须大于已有最大游标：%d", rec.Event.Cursor)
	}

	// 前沿也要恢复：重启前的发生时间仍能判迟到。
	if _, ok := restarted.Frontier(src); !ok {
		t.Fatal("重建后前沿必须存在")
	}
}

// TestSeedIncompleteRecordReported 覆盖索引重建的脏数据：缺键或缺指纹的记录
// 不能静默跳过，否则幂等会在没人察觉的情况下退化。
func TestSeedIncompleteRecordReported(t *testing.T) {
	g := newIng()
	rec := mustAccept(t, g.Ingest(managedSrc(), []Observation{baseObs()}))
	rec.Key = ""

	rej := newIng().Seed(managedSrc(), []Record{rec})
	if len(rej) != 1 || rej[0].Reason != ReasonSeedIncomplete {
		t.Fatalf("期望一条 %s，实际 %+v", ReasonSeedIncomplete, rej)
	}
}
