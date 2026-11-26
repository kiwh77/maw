package app_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
)

// seedWaitTask 直接落一条任务（真库、真 SQL），供 wait 用例读状态。
func seedWaitTask(t *testing.T, gdb, id, status string, deps []string) {
	t.Helper()
	err := db.WithTx(gdb, func(tx *sql.Tx) error {
		projects, e := db.ListProjects(tx)
		if e != nil {
			return e
		}
		if len(projects) == 0 {
			t.Fatal("no project registered by maw init")
		}
		return db.UpsertTask(tx, &db.Task{
			ProjectID: projects[0].ID, ID: id, Title: "task " + id, Status: status,
			Priority: 10, MergeOrder: 10, Deps: deps,
			WriteScope: []string{"internal/" + id + "/**"}, ReadScope: []string{},
			Acceptance: []string{"ok"},
		})
	})
	if err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestWaitDepsOfReturnsImmediately 覆盖 deps 已满足：立即成功、零等待。
func TestWaitDepsOfReturnsImmediately(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	seedWaitTask(t, gdb, "W-UP", "done", nil)
	seedWaitTask(t, gdb, "W-DOWN", "backlog", []string{"W-UP"})

	start := time.Now()
	res, err := app.Wait(repo, app.WaitInput{
		DepsOf: "W-DOWN", Timeout: 5 * time.Second, Poll: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("deps 已满足应立刻成功: %v", err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("结果应为 ok: %v", res)
	}
	if waited := res["waited_for"]; waited != "deps:W-DOWN" {
		t.Fatalf("waited_for=%v", waited)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("deps 已满足不该真等：%v", d)
	}
	if pending, _ := res["pending"].([]string); len(pending) != 0 {
		t.Fatalf("pending 应为空: %v", pending)
	}
}

// TestWaitTimesOutWithCode2 覆盖超时：退出码 2，且错误信息带出未满足的前置。
func TestWaitTimesOutWithCode2(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	seedWaitTask(t, gdb, "W-LATE", "ready", nil)
	seedWaitTask(t, gdb, "W-WAIT", "backlog", []string{"W-LATE"})

	start := time.Now()
	_, err := app.Wait(repo, app.WaitInput{
		DepsOf: "W-WAIT", Timeout: 300 * time.Millisecond, Poll: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("前置未完成必须超时")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("应为 errx.Error，得到 %v", err)
	}
	if ex.Code != app.WaitTimeoutExitCode {
		t.Fatalf("超时退出码应为 %d，得到 %d", app.WaitTimeoutExitCode, ex.Code)
	}
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Fatalf("不该提前返回：%v", d)
	}
}

// TestWaitTaskDone 覆盖等单题终态：done 立即成功，cancelled 判为不可满足。
func TestWaitTaskDone(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	seedWaitTask(t, gdb, "W-DONE", "done", nil)
	if _, err := app.Wait(repo, app.WaitInput{
		TaskID: "W-DONE", Timeout: time.Second, Poll: 20 * time.Millisecond,
	}); err != nil {
		t.Fatalf("done 应立即成功: %v", err)
	}

	seedWaitTask(t, gdb, "W-GONE", "cancelled", nil)
	if _, err := app.Wait(repo, app.WaitInput{
		TaskID: "W-GONE", Timeout: 200 * time.Millisecond, Poll: 20 * time.Millisecond,
	}); err == nil {
		t.Fatal("取消的任务不该被当成成功")
	}
}

// TestWaitRejectsMissingTarget 覆盖参数校验：两个目标都给（或都不给）必须报错。
func TestWaitRejectsMissingTarget(t *testing.T) {
	repo, _ := newStatusRepo(t)
	if _, err := app.Wait(repo, app.WaitInput{Timeout: time.Second}); err == nil {
		t.Fatal("既没 --task 也没 --deps-of 必须报错")
	}
}
