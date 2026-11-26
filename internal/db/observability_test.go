package db_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

func openObs(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "obs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	return store
}

func intPtr(v int64) *int64 { return &v }

func TestSourceSessionTurnUniquenessAndSharedTurn(t *testing.T) {
	store := openObs(t)
	if err := store.Write(func(tx *sql.Tx) error {
		return db.UpsertSourceSession(tx, db.SourceSession{
			SessionID: "sess_1", ProjectID: "p1", SourceNS: "observed/cursor-cli",
			ExternalID: "ext-sess-1", Adapter: "cursor-cli",
		})
	}); err != nil {
		t.Fatal(err)
	}
	err := store.Write(func(tx *sql.Tx) error {
		return db.UpsertSourceSession(tx, db.SourceSession{
			SessionID: "sess_2", ProjectID: "p1", SourceNS: "observed/cursor-cli",
			ExternalID: "ext-sess-1", Adapter: "cursor-cli",
		})
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("同 source_ns+external_id 应 UNIQUE 冲突，得到 %v", err)
	}

	if err := store.Write(func(tx *sql.Tx) error {
		if err := db.UpsertSourceTurn(tx, db.SourceTurn{
			TurnID: "turn_shared", SourceNS: "observed/cursor-cli", SessionID: "sess_1",
			SharedWith: []string{"ti_a", "ti_b"},
		}); err != nil {
			return err
		}
		got, err := db.GetSourceTurn(tx, "turn_shared")
		if err != nil {
			return err
		}
		if len(got.SharedWith) != 2 {
			t.Fatalf("shared_with=%v", got.SharedWith)
		}
		in, out := int64(10), int64(5)
		if err := db.PutUsageRecord(tx, db.UsageRecord{
			UsageID: "u1", SourceNS: "observed/cursor-cli", TurnID: "turn_shared",
			Epoch: 1, Scope: "model", Semantic: db.UsageDelta,
			InputTokens: &in, OutputTokens: &out, TotalTokens: intPtr(15),
			Quality: model.QualityObserved,
		}); err != nil {
			return err
		}
		list, err := db.ListUsageByTurn(tx, "turn_shared")
		if err != nil {
			return err
		}
		if len(list) != 1 {
			t.Fatalf("共享轮次只能一条用量，得到 %d", len(list))
		}
		return db.InsertRunSourceLink(tx, db.RunSourceLink{
			LinkID: "link_1", ProjectID: "p1", TurnID: "turn_shared", Attributed: false,
		})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUsageCumulativeResetAndUnknownNull(t *testing.T) {
	store := openObs(t)
	if err := store.Write(func(tx *sql.Tx) error {
		if err := db.UpsertSourceSession(tx, db.SourceSession{
			SessionID: "sess_u", SourceNS: "observed/cb", ExternalID: "e1",
		}); err != nil {
			return err
		}
		if err := db.UpsertSourceTurn(tx, db.SourceTurn{
			TurnID: "turn_u", SourceNS: "observed/cb", SessionID: "sess_u",
		}); err != nil {
			return err
		}
		in1, out1 := int64(100), int64(40)
		rec, err := db.ObserveCumulativeUsage(tx, db.UsageRecord{
			SourceNS: "observed/cb", TurnID: "turn_u", Scope: "model",
			Quality: model.QualityObserved,
		}, &in1, &out1)
		if err != nil {
			return err
		}
		if rec.Epoch != 1 || rec.ResetObserved {
			t.Fatalf("首代 epoch=%d reset=%v", rec.Epoch, rec.ResetObserved)
		}
		in2, out2 := int64(120), int64(50)
		rec, err = db.ObserveCumulativeUsage(tx, db.UsageRecord{
			SourceNS: "observed/cb", TurnID: "turn_u", Scope: "model",
			Quality: model.QualityObserved,
		}, &in2, &out2)
		if err != nil {
			return err
		}
		if rec.Semantic != db.UsageDelta || *rec.InputTokens != 20 || *rec.OutputTokens != 10 {
			t.Fatalf("增量应为 20/10，得到 %+v", rec)
		}
		in3, out3 := int64(5), int64(1)
		rec, err = db.ObserveCumulativeUsage(tx, db.UsageRecord{
			SourceNS: "observed/cb", TurnID: "turn_u", Scope: "model",
			Quality: model.QualityObserved,
		}, &in3, &out3)
		if err != nil {
			return err
		}
		if rec.Epoch != 2 || !rec.ResetObserved {
			t.Fatalf("重置后应 epoch=2 reset=true，得到 epoch=%d reset=%v", rec.Epoch, rec.ResetObserved)
		}
		zero := int64(0)
		if err := db.PutUsageRecord(tx, db.UsageRecord{
			UsageID: "bad", SourceNS: "observed/cb", TurnID: "turn_u", Epoch: 9,
			Scope: "model", Semantic: db.UsageDelta, Quality: model.QualityUnknown,
			InputTokens: &zero, OutputTokens: &zero,
		}); err == nil {
			t.Fatal("unknown+0 应被拒")
		}
		if err := db.PutUsageRecord(tx, db.UsageRecord{
			UsageID: "ok_unknown", SourceNS: "observed/cb", TurnID: "turn_u", Epoch: 9,
			Scope: "model", Semantic: db.UsageDelta, Quality: model.QualityUnknown,
		}); err != nil {
			return err
		}
		got, err := db.LatestUsage(tx, "observed/cb", "turn_u", "model")
		if err != nil {
			return err
		}
		if got.Epoch != 9 || got.InputTokens != nil || got.OutputTokens != nil {
			t.Fatalf("unknown 必须保持 null: %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionCheckpointAndNotificationDedup(t *testing.T) {
	store := openObs(t)
	if err := store.Write(func(tx *sql.Tx) error {
		dirtyFrom, dirtyTo := int64(10), int64(20)
		if err := db.UpsertProjectionState(tx, db.ProjectionState{
			Projection: "timeline", ProjectID: "p1", Version: 1,
			Cursor: 100, CompleteBefore: 90, DirtyFrom: &dirtyFrom, DirtyTo: &dirtyTo,
		}); err != nil {
			return err
		}
		if err := db.UpsertProjectionState(tx, db.ProjectionState{
			Projection: "timeline", ProjectID: "p1", Version: 2,
			Cursor: 150, CompleteBefore: 150,
		}); err != nil {
			return err
		}
		got, err := db.GetProjectionState(tx, "timeline", "p1")
		if err != nil {
			return err
		}
		if got.Version != 2 || got.Cursor != 150 || got.DirtyFrom != nil || got.DirtyTo != nil {
			t.Fatalf("检查点重放后应无脏范围: %+v", got)
		}
		ins, err := db.PutNotificationDelivery(tx, db.NotificationDelivery{
			NotificationID: "n1", ProjectID: "p1", EventID: "evt_abc",
			Channel: "desktop", Status: "pending",
		})
		if err != nil {
			return err
		}
		if !ins {
			t.Fatal("首次投递应插入")
		}
		ins, err = db.PutNotificationDelivery(tx, db.NotificationDelivery{
			NotificationID: "n2", ProjectID: "p1", EventID: "evt_abc",
			Channel: "desktop", Status: "pending",
		})
		if err != nil {
			return err
		}
		if ins {
			t.Fatal("同 event+channel 应去重")
		}
		n, err := db.GetNotificationByEventChannel(tx, "evt_abc", "desktop")
		if err != nil {
			return err
		}
		if n.NotificationID != "n1" {
			t.Fatalf("应保留首条通知身份，得到 %s", n.NotificationID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestUsageAttributionRoundTrip 说明用量行带得住归属：
// 项目、节点、Run、代际、会话、调用、用途、供应商模型与原始字段都能写能读。
func TestUsageAttributionRoundTrip(t *testing.T) {
	store := openObs(t)
	in, out := int64(120), int64(30)
	rec := db.NormalizeUsage(db.UsageRecord{
		UsageID: "u_attr", SourceNS: "pi", TurnID: "pi:run:s1:call:pc_1", Epoch: 1,
		Scope: db.UsageScopeCall, Semantic: db.UsageDelta,
		InputTokens: &in, OutputTokens: &out, Quality: model.QualityObserved,
		ProjectID: "p1", NodeID: "node_pi", RunID: "run_1", InstanceID: "ti_1",
		Generation: 2, CallID: "pc_1", SessionID: "pi:s1", Purpose: "worker",
		Provider: "anthropic", Model: "claude-x",
		CacheReadTokens: intPtr(80), CacheState: db.CacheStateVerified,
		CacheEvidence: "runtime:C30", Raw: map[string]any{"source_total_tokens": 150},
	})
	if err := store.Write(func(tx *sql.Tx) error { return db.PutUsageRecord(tx, rec) }); err != nil {
		t.Fatal(err)
	}
	if err := store.Read(func(tx *sql.Tx) error {
		got, err := db.ListUsageFacts(tx, db.UsageQuery{RunID: "run_1", CallID: "pc_1"})
		if err != nil {
			return err
		}
		if len(got) != 1 {
			t.Fatalf("按 Run＋调用过滤应命中 1 行，得到 %d", len(got))
		}
		u := got[0]
		if u.NodeID != "node_pi" || u.Generation != 2 || u.SessionID != "pi:s1" ||
			u.Purpose != "worker" || u.Model != "claude-x" || u.Provider != "anthropic" {
			t.Fatalf("归属字段丢失: %+v", u)
		}
		if u.CacheState != db.CacheStateVerified || u.CacheEvidence != "runtime:C30" {
			t.Fatalf("缓存归一证据丢失: %q/%q", u.CacheState, u.CacheEvidence)
		}
		if u.Raw["source_total_tokens"] == nil {
			t.Fatalf("原始字段应保留: %+v", u.Raw)
		}
		if u.TotalTokens == nil || *u.TotalTokens != 150 {
			t.Fatalf("total 应按 input+output 归一为 150，得到 %v", u.TotalTokens)
		}
		// 按用途过滤：别的用途不该命中。
		if rows, err := db.ListUsageFacts(tx, db.UsageQuery{RunID: "run_1", Purpose: "lead"}); err != nil || len(rows) != 0 {
			t.Fatalf("按用途过滤应无命中: %d %v", len(rows), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestNormalizeUsageKeepsUnknownAndCacheUnverified 说明归一不猜数值：
// 未知不填 0、缓存没证据标 unverified、total 对不上就丢弃。
func TestNormalizeUsageKeepsUnknownAndCacheUnverified(t *testing.T) {
	// quality=unknown 却带了 token：按契约置空，并写明原始值另有留存。
	u := db.NormalizeUsage(db.UsageRecord{
		SourceNS: "pi", TurnID: "t", Epoch: 1, Scope: db.UsageScopeCall,
		Semantic: db.UsageDelta, Quality: model.QualityUnknown,
		InputTokens: intPtr(9), OutputTokens: intPtr(1),
	})
	if u.InputTokens != nil || u.OutputTokens != nil {
		t.Fatalf("unknown 必须保持 null: %+v", u)
	}
	if !strings.Contains(u.Basis, "raw") {
		t.Fatalf("应说明原始值另有留存: %q", u.Basis)
	}

	// 缓存分量没有归一证据 → unverified，且不得并入 input。
	u = db.NormalizeUsage(db.UsageRecord{
		SourceNS: "pi", TurnID: "t2", Epoch: 1, Scope: db.UsageScopeCall,
		Semantic: db.UsageDelta, Quality: model.QualityObserved,
		InputTokens: intPtr(100), OutputTokens: intPtr(10), CacheReadTokens: intPtr(60),
	})
	if u.CacheState != db.CacheStateUnverified {
		t.Fatalf("缓存归一=%q 期望 unverified", u.CacheState)
	}
	if u.InputTokens == nil || *u.InputTokens != 100 || *u.TotalTokens != 110 {
		t.Fatalf("缓存不得并入 input 或 total: %+v", u)
	}

	// total 与分量不一致 → 丢弃 total，不猜。
	bad := int64(999)
	u = db.NormalizeUsage(db.UsageRecord{
		SourceNS: "pi", TurnID: "t3", Epoch: 1, Scope: db.UsageScopeCall,
		Semantic: db.UsageDelta, Quality: model.QualityObserved,
		InputTokens: intPtr(10), OutputTokens: intPtr(5), TotalTokens: &bad,
	})
	if u.TotalTokens != nil {
		t.Fatalf("不一致的 total 应丢弃: %v", *u.TotalTokens)
	}
	if !strings.Contains(u.Basis, "不一致") {
		t.Fatalf("应说明丢弃原因: %q", u.Basis)
	}
}

// TestRunUsageSummaryPrefersCallLevel 说明口径优先级：
// 有 call 级证据就只认 call 级，run 级汇总标 superseded 保留但不参与汇总。
func TestRunUsageSummaryPrefersCallLevel(t *testing.T) {
	store := openObs(t)
	if err := store.Write(func(tx *sql.Tx) error {
		// run 级兜底：先到。
		runIn, runOut := int64(1000), int64(200)
		runRec := db.NormalizeUsage(db.UsageRecord{
			UsageID: "u_run", SourceNS: "pi", TurnID: "pi:s:run:run_1", Epoch: 1,
			Scope: db.UsageScopeRun, Semantic: db.UsageDelta, Quality: model.QualityObserved,
			InputTokens: &runIn, OutputTokens: &runOut, RunID: "run_1", Generation: 1,
			ProjectID: "p1", Baseline: true,
		})
		if err := db.PutUsageRecord(tx, runRec); err != nil {
			return err
		}
		// 两次逐次调用：口径为 call。
		for i, tok := range []int64{10, 20} {
			out := int64(5)
			rec := db.NormalizeUsage(db.UsageRecord{
				UsageID: "u_call_" + string(rune('a'+i)), SourceNS: "pi",
				TurnID: "pi:s:call:pc_" + string(rune('a'+i)), Epoch: 1,
				Scope: db.UsageScopeCall, Semantic: db.UsageDelta,
				Quality: model.QualityObserved, InputTokens: &tok, OutputTokens: &out,
				RunID: "run_1", Generation: 1, CallID: "pc_" + string(rune('a'+i)),
				ProjectID: "p1",
			})
			if err := db.PutUsageRecord(tx, rec); err != nil {
				return err
			}
		}
		// call 级证据出现后，run 级降为「仅保留」。
		if n, err := db.SupersedeRunLevelUsage(tx, "run_1", 1); err != nil || n != 1 {
			return err
		}
		// 两次调用挂在同一个 Run 上（覆盖率分母来自关联）。
		for _, call := range []string{"pc_a", "pc_b"} {
			if err := db.EnsureRunSourceLink(tx, db.RunSourceLink{
				LinkID: "link_" + call, ProjectID: "p1", RunID: "run_1",
				TurnID: "pi:s:call:" + call, CallID: call, Generation: 1, Attributed: true,
			}); err != nil {
				return err
			}
		}
		sum, err := db.SummarizeRunUsage(tx, "run_1", 1)
		if err != nil {
			return err
		}
		if sum.Scope != db.UsageScopeCall {
			t.Fatalf("应采用 call 口径，得到 %q", sum.Scope)
		}
		if sum.InputTokens == nil || *sum.InputTokens != 30 {
			t.Fatalf("call 级 input 合计应为 30，得到 %+v", sum.InputTokens)
		}
		if sum.OutputTokens == nil || *sum.OutputTokens != 10 {
			t.Fatalf("call 级 output 合计应为 10，得到 %+v", sum.OutputTokens)
		}
		if sum.SupersededRunRows != 1 {
			t.Fatalf("run 级应记 1 行被取代，得到 %d", sum.SupersededRunRows)
		}
		if sum.Coverage == nil || *sum.Coverage != 1 {
			t.Fatalf("两次调用都有用量，覆盖率应为 1，得到 %v", sum.Coverage)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestRunUsageSummaryFallsBackToRunLevelAndNilCoverage 说明没有 call 级时用 run 级兜底，
// 且分母为 0 的覆盖率是 nil（不是 0）。
func TestRunUsageSummaryFallsBackToRunLevelAndNilCoverage(t *testing.T) {
	store := openObs(t)
	if err := store.Write(func(tx *sql.Tx) error {
		in, out := int64(7), int64(3)
		rec := db.NormalizeUsage(db.UsageRecord{
			UsageID: "u_run2", SourceNS: "codebuddy", TurnID: "cb:s:r", Epoch: 1,
			Scope: db.UsageScopeRun, Semantic: db.UsageDelta, Quality: model.QualityObserved,
			InputTokens: &in, OutputTokens: &out, RunID: "run_2", Generation: 1,
		})
		if err := db.PutUsageRecord(tx, rec); err != nil {
			return err
		}
		sum, err := db.SummarizeRunUsage(tx, "run_2", 1)
		if err != nil {
			return err
		}
		if sum.Scope != db.UsageScopeRun || sum.InputTokens == nil || *sum.InputTokens != 7 {
			t.Fatalf("应回落到 run 口径: %+v", sum)
		}
		if sum.Coverage != nil {
			t.Fatalf("没有调用分母时覆盖率必须是 nil，得到 %v", *sum.Coverage)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestUsageReplayKeepsSupersededFlag 说明重放不洗掉取代标记：
// 再写一次旧的 run 级记录，也不能把它重新拉回汇总。
func TestUsageReplayKeepsSupersededFlag(t *testing.T) {
	store := openObs(t)
	in, out := int64(5), int64(5)
	rec := db.NormalizeUsage(db.UsageRecord{
		UsageID: "u_rp", SourceNS: "pi", TurnID: "pi:s:run:run_rp", Epoch: 1,
		Scope: db.UsageScopeRun, Semantic: db.UsageDelta, Quality: model.QualityObserved,
		InputTokens: &in, OutputTokens: &out, RunID: "run_rp", Generation: 1,
	})
	if err := store.Write(func(tx *sql.Tx) error {
		if err := db.PutUsageRecord(tx, rec); err != nil {
			return err
		}
		_, err := db.SupersedeRunLevelUsage(tx, "run_rp", 1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(func(tx *sql.Tx) error { return db.PutUsageRecord(tx, rec) }); err != nil {
		t.Fatal(err)
	}
	if err := store.Read(func(tx *sql.Tx) error {
		rows, err := db.ListUsageFacts(tx, db.UsageQuery{RunID: "run_rp"})
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatalf("重放不该多出一行，得到 %d", len(rows))
		}
		if !rows[0].Superseded {
			t.Fatal("重放不得清掉 superseded 标记")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestNodeForRunReadsNodeSlotLease 说明节点归属有据可查：
// Run 的节点槽占用（resource_key=node:<id>#<slot>）就是节点归属的证据。
func TestNodeForRunReadsNodeSlotLease(t *testing.T) {
	store := openObs(t)
	if err := store.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO resource_leases(lease_id, kind, resource_key, project_id,
  instance_id, run_id, generation, holder, acquired_at)
VALUES (?,?,?,?,?,?,?,?,?)`, "lease_1", "node_slot", "node:node_pi#2", "p1",
			"ti_1", "run_node", 1, "coord", "2026-11-18T01:00:00Z")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Read(func(tx *sql.Tx) error {
		got, err := db.NodeForRun(tx, "run_node")
		if err != nil {
			return err
		}
		if got != "node_pi" {
			t.Fatalf("节点归属=%q 期望 node_pi", got)
		}
		if empty, err := db.NodeForRun(tx, "run_without_lease"); err != nil || empty != "" {
			t.Fatalf("没有占用时应留空，得到 %q %v", empty, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
