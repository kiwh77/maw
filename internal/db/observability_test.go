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
