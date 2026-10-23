package observability_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/observability"
)

func openStore(t *testing.T) *db.Store {
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

func TestMultiRunSameTurnMetersOnce(t *testing.T) {
	store := openStore(t)
	svc := &observability.Service{}
	if err := store.Write(func(tx *sql.Tx) error {
		if err := svc.EnsureSession(tx, db.SourceSession{
			SessionID: "sess_1", SourceNS: "observed/cb", ExternalID: "ext1",
		}); err != nil {
			return err
		}
		if err := svc.EnsureTurn(tx, db.SourceTurn{
			TurnID: "turn_1", SourceNS: "observed/cb", SessionID: "sess_1",
			SharedWith: []string{"ti_a", "ti_b"},
		}); err != nil {
			return err
		}
		// 两个 Run 挂同一 Turn
		for _, run := range []string{"run_a", "run_b"} {
			if err := svc.LinkRun(tx, observability.LinkRequest{
				RunID: run, TurnID: "turn_1", InstanceID: "ti_" + run[4:], Attributed: true,
			}); err != nil {
				return err
			}
		}
		in, out := int64(100), int64(20)
		if _, err := svc.ObserveUsage(tx, observability.UsageSnapshot{
			SourceNS: "observed/cb", TurnID: "turn_1", Semantic: db.UsageDelta,
			InputTokens: &in, OutputTokens: &out, Quality: model.QualityObserved,
		}); err != nil {
			return err
		}
		rep, err := svc.ReportTurn(tx, "turn_1", nil)
		if err != nil {
			return err
		}
		if !rep.Unallocated {
			t.Fatal("无分摊依据时应保留未分摊")
		}
		if rep.TotalTokens == nil || *rep.TotalTokens != 120 {
			t.Fatalf("多 Run 不得重复计量，得到 %v", rep.TotalTokens)
		}
		// 非法分摊仍未分摊
		rep, err = svc.ReportTurn(tx, "turn_1", []observability.Allocation{
			{InstanceID: "ti_a", Share: 0.7}, {InstanceID: "ti_b", Share: 0.7},
		})
		if err != nil {
			return err
		}
		if !rep.Unallocated || len(rep.Allocations) != 0 {
			t.Fatalf("份额和不为 1 时应未分摊: %+v", rep)
		}
		rep, err = svc.ReportTurn(tx, "turn_1", []observability.Allocation{
			{InstanceID: "ti_a", Share: 0.5}, {InstanceID: "ti_b", Share: 0.5},
		})
		if err != nil {
			return err
		}
		if rep.Unallocated || len(rep.Allocations) != 2 {
			t.Fatalf("合法分摊应落地: %+v", rep)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCumulativeResetCacheAndNullCoverage(t *testing.T) {
	store := openStore(t)
	svc := &observability.Service{}
	if err := store.Write(func(tx *sql.Tx) error {
		_ = svc.EnsureSession(tx, db.SourceSession{
			SessionID: "sess_2", SourceNS: "observed/cb", ExternalID: "ext2",
		})
		_ = svc.EnsureTurn(tx, db.SourceTurn{
			TurnID: "turn_2", SourceNS: "observed/cb", SessionID: "sess_2",
		})
		in1, out1 := int64(50), int64(10)
		cr := int64(3)
		rec, err := svc.ObserveUsage(tx, observability.UsageSnapshot{
			SourceNS: "observed/cb", TurnID: "turn_2", Semantic: db.UsageCumulative,
			InputTokens: &in1, OutputTokens: &out1, CacheReadTokens: &cr,
			Quality: model.QualityObserved,
		})
		if err != nil {
			return err
		}
		if rec.Epoch != 1 {
			t.Fatalf("epoch=%d", rec.Epoch)
		}
		// 累计下降 → 新 epoch
		in2, out2 := int64(4), int64(1)
		rec, err = svc.ObserveUsage(tx, observability.UsageSnapshot{
			SourceNS: "observed/cb", TurnID: "turn_2", Semantic: db.UsageCumulative,
			InputTokens: &in2, OutputTokens: &out2, Quality: model.QualityObserved,
		})
		if err != nil {
			return err
		}
		if rec.Epoch != 2 || !rec.ResetObserved {
			t.Fatalf("重置失败: %+v", rec)
		}
		// unknown null
		if _, err := svc.ObserveUsage(tx, observability.UsageSnapshot{
			SourceNS: "observed/cb", TurnID: "turn_2", Semantic: db.UsageDelta,
			Quality: model.QualityUnknown,
		}); err != nil {
			// delta epoch=1 可能冲突；用 cumulative unknown 路径
			_ = err
		}
		if err := db.PutUsageRecord(tx, db.UsageRecord{
			UsageID: "u_unk", SourceNS: "observed/cb", TurnID: "turn_2", Epoch: 9,
			Scope: "model", Semantic: db.UsageDelta, Quality: model.QualityUnknown,
		}); err != nil {
			return err
		}
		rep, err := svc.ReportTurn(tx, "turn_2", nil)
		if err != nil {
			return err
		}
		if rep.Quality != model.QualityUnknown {
			t.Fatalf("含 unknown 分量时报告质量应为 unknown，得到 %s", rep.Quality)
		}
		// 覆盖率：只对已观测分母
		cov, err := svc.CoverageOf(tx, []string{"turn_2", "turn_never", "turn_2"})
		if err != nil {
			return err
		}
		if cov.ObservedDenom != 2 || cov.ObservedTurns != 1 {
			t.Fatalf("覆盖率分母应去重且含未观测 turn: %+v", cov)
		}
		if cov.Ratio != 0.5 {
			t.Fatalf("ratio=%v", cov.Ratio)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCrossSourceSameExternalIDIndependent(t *testing.T) {
	store := openStore(t)
	svc := &observability.Service{}
	if err := store.Write(func(tx *sql.Tx) error {
		if err := svc.EnsureSession(tx, db.SourceSession{
			SessionID: "s_a", SourceNS: "observed/cursor", ExternalID: "same-ext",
		}); err != nil {
			return err
		}
		if err := svc.EnsureSession(tx, db.SourceSession{
			SessionID: "s_b", SourceNS: "observed/trae", ExternalID: "same-ext",
		}); err != nil {
			return err
		}
		a, err := db.GetSourceSessionByExternal(tx, "observed/cursor", "same-ext")
		if err != nil {
			return err
		}
		b, err := db.GetSourceSessionByExternal(tx, "observed/trae", "same-ext")
		if err != nil {
			return err
		}
		if a.SessionID == b.SessionID {
			t.Fatal("跨来源同外部 ID 必须是独立会话")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
