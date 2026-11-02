package app

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// TestSubmitReviewMergeShareDeliveryCase 覆盖验收一：
// submit／reject／block／merge 都走 applyDeliveryCase，库里能追到同一条交付。
func TestSubmitReviewMergeShareDeliveryCase(t *testing.T) {
	f := newFixture(t)
	const id = "VN-D1"
	f.seedTask(id, "ready", "", []string{"mod_d1/**"})
	_ = os.MkdirAll(filepath.Join(f.repo, "mod_d1"), 0o755)

	t.Setenv("MAW_AGENT", "cursor-author")
	if _, err := Next(f.repo, "cursor-author", false, ""); err != nil {
		t.Fatalf("next: %v", err)
	}
	wt := filepath.Join(f.repo, ".worktrees", "cursor-author")
	writeAndCommit(t, wt, "mod_d1/a.txt", "a\n", "d1")

	res, err := Submit(wt, id, false)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	deliveryID, _ := res["delivery_id"].(string)
	if deliveryID == "" || !strings.HasPrefix(deliveryID, "dlv_") {
		t.Fatalf("submit 必须冻结交付身份，得到 %v", res["delivery_id"])
	}
	if res["strategy"] != manualCompatStrategy {
		t.Fatalf("人工兼容策略应为 %s，得到 %v", manualCompatStrategy, res["strategy"])
	}
	if res["status"] != "merging" { // 默认 review=auto
		t.Fatalf("auto review 后应为 merging，得到 %v", res["status"])
	}

	// 拒绝路径：把状态拨回 in_review，再用另一 agent 走 reject。
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		task, e := db.GetTask(tx, f.pc.ID(), id)
		if e != nil || task == nil {
			return e
		}
		task.Status = "in_review"
		task.Revision++
		return db.UpsertTask(tx, task)
	})
	if err != nil {
		t.Fatalf("reset in_review: %v", err)
	}
	t.Setenv("MAW_AGENT", "cursor-reviewer")
	if _, err := Register(f.repo, "cursor-reviewer", "reviewer"); err != nil {
		t.Fatalf("register reviewer: %v", err)
	}
	rej, err := Review(f.repo, id, false, "need fix")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rej["delivery_id"] != deliveryID {
		t.Fatalf("reject 必须作用于同一交付：submit=%s reject=%v", deliveryID, rej["delivery_id"])
	}
	if rej["status"] != "in_progress" {
		t.Fatalf("reject 后应回 in_progress，得到 %v", rej["status"])
	}

	// 再提交后合入；交付清单应记 merged（可能 supersede 首条交付）。
	writeAndCommit(t, wt, "mod_d1/a.txt", "a2\n", "d1-fix")
	t.Setenv("MAW_AGENT", "cursor-author")
	if _, err := Submit(wt, id, false); err != nil {
		t.Fatalf("re-submit: %v", err)
	}
	mergeRes, err := Merge(f.repo, true, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if ok, _ := mergeRes["ok"].(bool); !ok {
		t.Fatalf("merge 应成功：%v", mergeRes)
	}
	task := f.task(id)
	if task.Status != "done" {
		t.Fatalf("合入后任务应为 done，得到 %s", task.Status)
	}
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		latest, e := latestDelivery(tx, f.pc.ID(), task)
		if e != nil || latest == nil {
			return e
		}
		if !latest.Delivery.Merge.Merged {
			t.Fatalf("合入后交付必须记 merged：%+v", latest.Delivery.Merge)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("check delivery merge: %v", err)
	}
}

// TestBlockUsesDeliveryCase 阻塞也走同一验收用例（有交付时写入评审）。
func TestBlockUsesDeliveryCase(t *testing.T) {
	f := newFixture(t)
	const id = "VN-D1B"
	f.seedTask(id, "ready", "", []string{"mod_d1b/**"})
	_ = os.MkdirAll(filepath.Join(f.repo, "mod_d1b"), 0o755)
	t.Setenv("MAW_AGENT", "cursor-blk")
	if _, err := Next(f.repo, "cursor-blk", false, ""); err != nil {
		t.Fatalf("next: %v", err)
	}
	wt := filepath.Join(f.repo, ".worktrees", "cursor-blk")
	writeAndCommit(t, wt, "mod_d1b/x.txt", "x\n", "blk")
	sub, err := Submit(wt, id, false)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// 拨回 in_progress 再 block（owner 才能 block）。
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		task, e := db.GetTask(tx, f.pc.ID(), id)
		if e != nil || task == nil {
			return e
		}
		task.Status = "in_progress"
		task.Revision++
		return db.UpsertTask(tx, task)
	})
	if err != nil {
		t.Fatal(err)
	}
	blk, err := Block(wt, "blocked for test", id)
	if err != nil {
		t.Fatalf("block: %v", err)
	}
	if blk["delivery_id"] != sub["delivery_id"] {
		t.Fatalf("block 应挂同一交付：%v vs %v", blk["delivery_id"], sub["delivery_id"])
	}
	if f.task(id).Status != "blocked" {
		t.Fatalf("期望 blocked，得到 %s", f.task(id).Status)
	}
}

// TestMergePruneBlockedOnUnknownOrEvidenceMismatch 覆盖验收二：
// unknown／残留进程／证据不匹配时不能 merge／prune。
func TestMergePruneBlockedOnUnknownOrEvidenceMismatch(t *testing.T) {
	f := newFixture(t)
	const id = "VN-D2"
	f.seedTask(id, "ready", "", []string{"mod_d2/**"})
	_ = os.MkdirAll(filepath.Join(f.repo, "mod_d2"), 0o755)

	t.Setenv("MAW_AGENT", "cursor-d2")
	if _, err := Next(f.repo, "cursor-d2", false, ""); err != nil {
		t.Fatalf("next: %v", err)
	}
	wt := filepath.Join(f.repo, ".worktrees", "cursor-d2")
	writeAndCommit(t, wt, "mod_d2/b.txt", "b\n", "d2")
	if _, err := Submit(wt, id, false); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// 证据不匹配：改掉验证指纹，merge 必须拒绝。
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		task, e := db.GetTask(tx, f.pc.ID(), id)
		if e != nil || task == nil {
			return e
		}
		rec, e := latestDelivery(tx, f.pc.ID(), task)
		if e != nil || rec == nil {
			return e
		}
		_, e = tx.Exec(`UPDATE delivery_verifications SET fingerprint = ? WHERE delivery_id = ?`,
			"stale-fingerprint", string(rec.Delivery.DeliveryID))
		return e
	})
	if err != nil {
		t.Fatalf("stale fingerprint: %v", err)
	}
	if err := assertDeliveryMergeReady(f.pc, f.task(id)); err == nil {
		t.Fatal("证据不匹配时 merge 准备检查必须失败")
	}
	mergeRes, err := Merge(f.repo, true, false)
	if err != nil {
		t.Fatalf("merge 调用本身不应炸掉：%v", err)
	}
	if ok, _ := mergeRes["ok"].(bool); ok {
		t.Fatalf("证据不匹配时 merge 不得成功：%v", mergeRes)
	}

	// 恢复指纹后，把 Run 标成失败且进程未确认停止：merge 必须拒绝。
	task := f.task(id)
	runID := extraString(task, extraRunID)
	if runID == "" {
		t.Fatal("submit 后应记录 run_id")
	}
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		rec, e := latestDelivery(tx, f.pc.ID(), task)
		if e != nil || rec == nil {
			return e
		}
		if _, e := tx.Exec(`UPDATE delivery_verifications SET fingerprint = ? WHERE delivery_id = ?`,
			rec.Fingerprint, string(rec.Delivery.DeliveryID)); e != nil {
			return e
		}
		_, e = tx.Exec(`UPDATE runs SET phase = ?, process_stop_confirmed = 0,
  reconciliation_state = ?, delivery_frozen = 0 WHERE run_id = ?`,
			string(model.PhaseFailed), string(model.ReconciliationConfirmed), runID)
		return e
	})
	if err != nil {
		t.Fatalf("mark stop unconfirmed: %v", err)
	}
	if err := assertDeliveryMergeReady(f.pc, f.task(id)); err == nil {
		t.Fatal("残留进程未确认时 merge 必须失败")
	}

	// unknown 核实状态同样挡合入。
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, e := tx.Exec(`UPDATE runs SET phase = ?, process_stop_confirmed = 1,
  reconciliation_state = ?, delivery_frozen = 0 WHERE run_id = ?`,
			string(model.PhaseCompleted), string(model.ReconciliationUnknown), runID)
		return e
	})
	if err != nil {
		t.Fatalf("mark unknown: %v", err)
	}
	if err := assertDeliveryMergeReady(f.pc, f.task(id)); err == nil {
		t.Fatal("reconciliation unknown 时不能 merge")
	}

	// 占用未冻结时，workspaceHoldReason 必须挡住 prune/reset。
	orphan := filepath.Join(f.repo, ".worktrees", "orphan-hold")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		if _, e := db.AcquireLease(tx, db.LeaseSpec{
			ProjectID: f.pc.ID(), InstanceID: task.InstanceID, RunID: runID,
			Generation: 9, Holder: "cursor-d2",
		}, db.Resource{Kind: model.LeaseWorkspace, Path: orphan}); e != nil {
			return e
		}
		reason, blocked := workspaceHoldReason(tx, orphan)
		if !blocked {
			t.Fatalf("unknown 占用应阻止 prune，reason=%q", reason)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("hold check: %v", err)
	}
	pruneRes, err := Prune(f.repo, false, true)
	if err != nil {
		t.Fatalf("prune preview: %v", err)
	}
	// 预览不得把仍占用的 orphan 列为可删。
	for _, wt := range asStringSlice(pruneRes["worktrees"]) {
		if wt == orphan {
			t.Fatalf("prune 不得列出仍占用的目录 %s；holds=%v", orphan, pruneRes["blocked_holds"])
		}
	}
}

func asStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// TestManualCompatStrategyCompletesWithoutGates 覆盖「人工兼容任务按明确策略完成」：
// 无 gates 时走 KindManual 验证 + auto review，仍能 merge done。
func TestManualCompatStrategyCompletesWithoutGates(t *testing.T) {
	f := newFixture(t)
	const id = "VN-D3"
	f.seedTask(id, "ready", "", []string{"mod_d3/**"})
	_ = os.MkdirAll(filepath.Join(f.repo, "mod_d3"), 0o755)

	t.Setenv("MAW_AGENT", "cursor-d3")
	if _, err := Next(f.repo, "cursor-d3", false, ""); err != nil {
		t.Fatalf("next: %v", err)
	}
	wt := filepath.Join(f.repo, ".worktrees", "cursor-d3")
	writeAndCommit(t, wt, "mod_d3/c.txt", "c\n", "d3")
	res, err := Submit(wt, id, false)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res["strategy"] != manualCompatStrategy {
		t.Fatalf("策略 %v", res["strategy"])
	}
	task := f.task(id)
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		rec, e := latestDelivery(tx, f.pc.ID(), task)
		if e != nil || rec == nil {
			t.Fatalf("应有交付: %v", e)
			return e
		}
		ready, e := db.DeliveryReadiness(tx, string(rec.Delivery.DeliveryID))
		if e != nil {
			return e
		}
		if !ready.PassingVerification || !ready.ApprovedReview {
			t.Fatalf("无 gates 人工兼容应具备 manual 验证 + auto 评审：%+v", ready)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mergeRes, err := Merge(f.repo, true, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if ok, _ := mergeRes["ok"].(bool); !ok {
		t.Fatalf("merge 应成功：%v", mergeRes)
	}
	if f.task(id).Status != "done" {
		t.Fatalf("期望 done，得到 %s", f.task(id).Status)
	}
}

func writeAndCommit(t *testing.T, wt, rel, content, msg string) {
	t.Helper()
	path := filepath.Join(wt, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = wt
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", rel)
	run("commit", "-m", msg)
}
