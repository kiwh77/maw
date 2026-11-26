package app

import (
	"database/sql"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/projection"
)

// TestStatsReportWritesTurnsAndUsage 覆盖外部 Agent 主动上报：轮次、用量、工程归属都要落库。
func TestStatsReportWritesTurnsAndUsage(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("VN-STAT-1", "in_progress", "cursor-stat", []string{"mod/**"})
	t.Setenv("MAW_AGENT", "cursor-stat")

	input, output := int64(100), int64(20)
	res, err := StatsReport(f.repo, StatsReportInput{
		TaskID: task.ID, Client: "claude-code", Turns: 2,
		InputTokens: &input, OutputTokens: &output,
	})
	if err != nil {
		t.Fatalf("stats report: %v", err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("result=%v", res)
	}

	var sessions, turns, usage, links, inSum, outSum int64
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		if e := tx.QueryRow(`SELECT COUNT(*) FROM source_sessions WHERE project_id = ?`, f.pc.ID()).Scan(&sessions); e != nil {
			return e
		}
		if e := tx.QueryRow(`SELECT COUNT(*) FROM source_turns WHERE source_ns = 'claude-code'`).Scan(&turns); e != nil {
			return e
		}
		if e := tx.QueryRow(`SELECT COUNT(*) FROM usage_records WHERE source_ns = 'claude-code'`).Scan(&usage); e != nil {
			return e
		}
		if e := tx.QueryRow(`SELECT COUNT(*) FROM run_source_links WHERE project_id = ?`, f.pc.ID()).Scan(&links); e != nil {
			return e
		}
		if e := tx.QueryRow(`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0) FROM usage_records WHERE source_ns = 'claude-code'`).Scan(&inSum, &outSum); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || turns != 2 || usage != 2 || links != 2 {
		t.Fatalf("sessions=%d turns=%d usage=%d links=%d", sessions, turns, usage, links)
	}
	if inSum != input || outSum != output {
		t.Fatalf("token sums=%d/%d want %d/%d", inSum, outSum, input, output)
	}

	// 报表侧也要能看到这两轮：否则落库了但统计点读不到，等于没写。
	var rep *analytics.Report
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		now := time.Now()
		var e error
		rep, e = (&analytics.Service{}).Report(tx, analytics.Query{
			ProjectID: f.pc.ID(),
			Window:    projection.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)},
		}, nil, projection.Cursors{})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Usage.TurnCount != 2 || rep.Usage.Tokens.InputTokens == nil || *rep.Usage.Tokens.InputTokens != input {
		t.Fatalf("report usage=%+v", rep.Usage)
	}
}

// TestStatsReportWithoutTokensKeepsTurns 覆盖「拿不到 token」：轮次仍记账，但不写 0 用量。
func TestStatsReportWithoutTokensKeepsTurns(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("VN-STAT-2", "in_progress", "cursor-stat", []string{"mod/**"})
	t.Setenv("MAW_AGENT", "cursor-stat")

	if _, err := StatsReport(f.repo, StatsReportInput{TaskID: task.ID, Client: "codex", Turns: 3}); err != nil {
		t.Fatalf("stats report: %v", err)
	}

	var turns, usage int64
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		if e := tx.QueryRow(`SELECT COUNT(*) FROM source_turns WHERE source_ns = 'codex'`).Scan(&turns); e != nil {
			return e
		}
		return tx.QueryRow(`SELECT COUNT(*) FROM usage_records WHERE source_ns = 'codex'`).Scan(&usage)
	})
	if err != nil {
		t.Fatal(err)
	}
	if turns != 3 || usage != 0 {
		t.Fatalf("turns=%d usage=%d", turns, usage)
	}
}

// TestStatsReportRejectsForeignTask 覆盖归属红线：只能补自己持有的任务。
func TestStatsReportRejectsForeignTask(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("VN-STAT-3", "in_progress", "cursor-other", []string{"mod/**"})
	t.Setenv("MAW_AGENT", "cursor-stat")

	if _, err := StatsReport(f.repo, StatsReportInput{TaskID: task.ID, Turns: 1}); err == nil {
		t.Fatal("应为他人任务的上报被拒绝")
	}
}
