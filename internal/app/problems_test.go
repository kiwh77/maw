package app

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/db"
)

// 装配冒烟：状态库里的 blocked 任务应经 loadBoardSnapshot 聚合进 snapshot.problems。
func TestLoadBoardSnapshotAssemblesProblems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maw.db")
	if err := db.Init(path); err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(path, func(tx *sql.Tx) error {
		if err := db.UpsertTask(tx, &db.Task{
			ProjectID: "p1", ID: "T-1", Title: "阻塞题", Status: "blocked",
			BlockedReason: "等上游契约", Heartbeat: db.UTCNow(),
		}); err != nil {
			return err
		}
		return db.UpsertTask(tx, &db.Task{
			ProjectID: "p1", ID: "T-2", Title: "就绪题", Status: "ready",
			Deps: []string{"T-1"},
		})
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := loadBoardSnapshot(path, "", "", "", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Problems) != 2 {
		t.Fatalf("装配后应聚合出 2 条问题（blocked + dep-wait）：%+v", snap.Problems)
	}
	var kinds []string
	for _, p := range snap.Problems {
		kinds = append(kinds, p.Kind)
	}
	if kinds[0] != board.ProblemKindBlocked {
		t.Fatalf("blocked 应排在前（severity 高）：%+v", snap.Problems)
	}
	if kinds[1] != board.ProblemKindDepWait {
		t.Fatalf("ready 引用 blocked 上游应产生 dep-wait：%+v", snap.Problems)
	}
}
