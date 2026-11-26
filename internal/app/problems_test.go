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
	// 装配后的问题是两类事实的并集：任务态问题（blocked／dep-wait）与
	// Router 对账结果（router-idle：这条阻塞还没有人接管）。
	kinds := map[string]int{}
	var order []string
	for _, p := range snap.Problems {
		kinds[p.Kind]++
		order = append(order, p.Kind)
	}
	if kinds[board.ProblemKindBlocked] != 1 {
		t.Fatalf("应聚合出 1 条 blocked：%+v", snap.Problems)
	}
	if kinds[board.ProblemKindDepWait] != 1 {
		t.Fatalf("应聚合出 1 条 dep-wait：%+v", snap.Problems)
	}
	if kinds[board.ProblemKindRouterIdle] != 1 {
		t.Fatalf("阻塞任务没有 Router 作业时应聚合出 1 条 router-idle：%+v", snap.Problems)
	}
	present := func(kind string) bool { return kinds[kind] == 1 }
	if !present(board.ProblemKindBlocked) || !present(board.ProblemKindDepWait) {
		t.Fatalf("问题聚合缺项：%v", order)
	}
}
