package app

import (
	"database/sql"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/systemagent"
)

// seedQueueTask 落一条带依赖 / 心跳的原始任务，供队列信号事实采集用。
func seedQueueTask(t *testing.T, f *fixture, task *db.Task) {
	t.Helper()
	task.ProjectID = f.pc.ID()
	if task.Title == "" {
		task.Title = "task " + task.ID
	}
	if task.Priority == 0 {
		task.Priority = 10
	}
	if task.MergeOrder == 0 {
		task.MergeOrder = 10
	}
	if task.Acceptance == nil {
		task.Acceptance = []string{"ok"}
	}
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return db.UpsertTask(tx, task)
	}); err != nil {
		t.Fatalf("seed %s: %v", task.ID, err)
	}
}

// TestHubSignalFactsQueueSignals 覆盖验收：从真实库采集五类队列卡死证据，
// 并确认它们全部只上报（不产出作业）。
func TestHubSignalFactsQueueSignals(t *testing.T) {
	f := newFixture(t)
	old := time.Now().UTC().Add(-2 * time.Hour)

	// ready 题 + 无节点登记 → no_capable_node。
	seedQueueTask(t, f, &db.Task{ID: "T-READY", Status: "ready"})
	// 依赖环 → dep_deadlock。
	seedQueueTask(t, f, &db.Task{ID: "T-C", Status: "backlog", Deps: []string{"T-D"}})
	seedQueueTask(t, f, &db.Task{ID: "T-D", Status: "backlog", Deps: []string{"T-C"}})
	// in_progress 且从未心跳、没有 Run → claimed_not_started。
	seedQueueTask(t, f, &db.Task{ID: "T-RUN", Status: "in_progress"})

	inst := f.task("T-READY").InstanceID
	stamp := old.Format(time.RFC3339)
	recent := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		// 同一实例三条 failed Run → repeated_failure（必须在统计窗口内）。
		for gen := 1; gen <= 3; gen++ {
			if _, e := tx.Exec(`INSERT INTO runs(
run_id, project_id, instance_id, version_no, generation, phase, reconciliation_state,
created_at, updated_at, started_at, finished_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				"run-qf-"+string(rune('0'+gen)), f.pc.ID(), inst, 1, gen,
				"failed", "confirmed", stamp, recent, stamp, recent); e != nil {
				return e
			}
		}
		// pending 且租约过期两小时的派发操作 → dispatch_op_stalled。
		_, e := tx.Exec(`INSERT INTO operations(operation_id, project_id, kind, run_id, instance_id,
idempotency_key, request_digest, target_phase, result, coordinator, lease_expires_at, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			"op-qf", f.pc.ID(), "dispatch", "", inst, "key-qf", "digest-qf", "",
			"pending", "", stamp, stamp, stamp)
		return e
	})
	if err != nil {
		t.Fatalf("seed runs/operations: %v", err)
	}

	cfg := f.pc.Config.SystemAgent
	facts := newHubSignalFacts(f.pc.GlobalDB, cfg)
	signals := systemagent.DetectSystemSignals(facts, cfg, time.Now().UTC())

	want := map[systemagent.SignalKind]bool{
		systemagent.SignalDispatchOpStalled: false,
		systemagent.SignalClaimedNotStarted: false,
		systemagent.SignalDepDeadlock:       false,
		systemagent.SignalNoCapableNode:     false,
		systemagent.SignalRepeatedFailure:   false,
	}
	for _, s := range signals {
		if _, ok := want[s.Kind]; !ok {
			continue
		}
		if s.JobKind != "" || s.Decision["report_only"] != true {
			t.Fatalf("%s 应只上报: %+v", s.Kind, s)
		}
		want[s.Kind] = true
	}
	for kind, got := range want {
		if !got {
			t.Fatalf("缺信号 %s，实际 %+v", kind, signals)
		}
	}
}

// TestLeadSignalsJSONIncludesQueueKinds 覆盖验收 3：maw lead signals 输出含新 kind 与 reason。
func TestLeadSignalsJSONIncludesQueueKinds(t *testing.T) {
	f := newFixture(t)
	seedQueueTask(t, f, &db.Task{ID: "T-READY", Status: "ready"})

	res, err := LeadSignals(f.repo, false)
	if err != nil {
		t.Fatalf("LeadSignals: %v", err)
	}
	rows, _ := res["signals"].([]map[string]any)
	var found bool
	for _, row := range rows {
		if row["kind"] == string(systemagent.SignalNoCapableNode) {
			found = true
			if reason, _ := row["reason"].(string); reason == "" {
				t.Fatalf("no_capable_node 应带 reason: %v", row)
			}
			if row["job_kind"] != "" {
				t.Fatalf("no_capable_node 不应产出作业: %v", row)
			}
		}
	}
	if !found {
		t.Fatalf("输出缺 no_capable_node: %v", rows)
	}
}
