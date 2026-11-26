package retention_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/retention"
)

func TestDefaultPolicyExplicit(t *testing.T) {
	p := retention.DefaultPolicy()
	if p.LogMaxAge <= 0 || p.ArtifactMaxAge <= 0 || p.RunDirMaxAge <= 0 {
		t.Fatalf("default ages must be explicit: %+v", p)
	}
	if !p.ProtectOccupied || !p.ProtectUnknown || !p.ProtectPendingJournal || !p.ProtectDeliveryEvidence {
		t.Fatalf("default protections must be on: %+v", p)
	}
}

func TestProtectOccupiedUnknownPendingDelivery(t *testing.T) {
	p := retention.DefaultPolicy()
	aged := retention.Candidate{Kind: retention.TargetRunDir, Path: "/x", RunID: "run_1", Age: 30 * 24 * time.Hour}

	cases := []struct {
		name string
		g    retention.RunGuard
		want retention.ProtectionReason
	}{
		{"occupied", retention.RunGuard{Occupies: true, Terminal: true}, retention.ReasonOccupied},
		{"unknown", retention.RunGuard{ReconciliationUnknown: true, Terminal: true}, retention.ReasonUnknown},
		{"pending journal", retention.RunGuard{PendingJournal: 3, Terminal: true}, retention.ReasonPendingJournal},
		{"delivery", retention.RunGuard{DeliveryRefs: 1, Terminal: true}, retention.ReasonDeliveryEvidence},
	}
	for _, tc := range cases {
		d := retention.Evaluate(p, aged, tc.g)
		if d.Action != retention.ActionRetain {
			t.Fatalf("%s: want retain, got %s", tc.name, d.Action)
		}
		found := false
		for _, r := range d.Reasons {
			if r == tc.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: reasons=%v", tc.name, d.Reasons)
		}
	}
}

func TestNoForceDeleteBypassForUnknown(t *testing.T) {
	p := retention.DefaultPolicy()
	p.ProtectUnknown = true
	d := retention.Evaluate(p, retention.Candidate{
		Kind: retention.TargetRunDir, Path: "/r", RunID: "run_u", Age: 365 * 24 * time.Hour,
	}, retention.RunGuard{ReconciliationUnknown: true, Terminal: true})
	if d.Action != retention.ActionRetain {
		t.Fatal("unknown must never purge")
	}
}

func TestPurgeOnlyWhenAgedAndUnprotected(t *testing.T) {
	p := retention.DefaultPolicy()
	young := retention.Evaluate(p, retention.Candidate{
		Kind: retention.TargetLog, Path: "/l", RunID: "run_1", Age: time.Hour,
	}, retention.RunGuard{Terminal: true})
	if young.Action != retention.ActionRetain {
		t.Fatal("young log must retain")
	}
	old := retention.Evaluate(p, retention.Candidate{
		Kind: retention.TargetLog, Path: "/l", RunID: "run_1", Age: 30 * 24 * time.Hour,
	}, retention.RunGuard{Terminal: true, ProcessStopConfirmed: true})
	if old.Action != retention.ActionPurge {
		t.Fatalf("aged unprotected log should purge: %s", old.Explain())
	}
}

func TestPlanExecuteIdempotentAndQueryCleanedVsMissing(t *testing.T) {
	root := t.TempDir()
	ledgerDir := filepath.Join(root, "ledger")
	runDir := filepath.Join(root, "runs", "run_old")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(runDir, "stdout.log")
	if err := os.WriteFile(logPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledger, err := retention.OpenLedger(ledgerDir)
	if err != nil {
		t.Fatal(err)
	}
	svc := &retention.Service{
		Policy: retention.DefaultPolicy(),
		Ledger: ledger,
		Root:   root,
		Guard: func(runID string) (retention.RunGuard, error) {
			return retention.RunGuard{RunID: runID, Terminal: true, ProcessStopConfirmed: true}, nil
		},
	}

	cands := []retention.Candidate{{
		Kind: retention.TargetLog, Path: logPath, RunID: "run_old", Age: 30 * 24 * time.Hour,
	}}
	guards := map[string]retention.RunGuard{
		"run_old": {RunID: "run_old", Terminal: true, ProcessStopConfirmed: true},
	}
	intents, err := svc.Plan(cands, guards)
	if err != nil || len(intents) != 1 {
		t.Fatalf("plan: %v %#v", err, intents)
	}

	in, err := svc.Execute(intents[0].IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if in.State != retention.IntentDeleted {
		t.Fatalf("state=%s", in.State)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatal("log should be removed")
	}

	// 幂等重试
	in2, err := svc.Execute(intents[0].IntentID)
	if err != nil || in2.State != retention.IntentDeleted {
		t.Fatalf("idempotent retry: %v %#v", err, in2)
	}

	pres, _, err := svc.Query(retention.TargetLog, logPath, guards["run_old"])
	if err != nil || pres != retention.PresenceCleaned {
		t.Fatalf("want cleaned, got %s err=%v", pres, err)
	}

	// 从未见过的路径：missing，不是 cleaned
	pres, gotIntent, err := svc.Query(retention.TargetLog, filepath.Join(root, "runs", "never", "x.log"), retention.RunGuard{})
	if err != nil || pres != retention.PresenceMissing || gotIntent != nil {
		t.Fatalf("want missing without intent, got %s %#v", pres, gotIntent)
	}
}

func TestExecuteRespectsNewOccupancyAndRetriesAfterFailure(t *testing.T) {
	root := t.TempDir()
	ledger, _ := retention.OpenLedger(filepath.Join(root, "ledger"))
	target := filepath.Join(root, "runs", "run_race")
	_ = os.MkdirAll(target, 0o755)
	_ = os.WriteFile(filepath.Join(target, "journal.jsonl"), []byte("{}"), 0o644)

	occupied := true
	svc := &retention.Service{
		Policy: retention.DefaultPolicy(),
		Ledger: ledger,
		Root:   root,
		Guard: func(string) (retention.RunGuard, error) {
			return retention.RunGuard{
				RunID: "run_race", Terminal: true, Occupies: occupied, PendingJournal: 0,
			}, nil
		},
	}
	cands := []retention.Candidate{{
		Kind: retention.TargetRunDir, Path: target, RunID: "run_race", Age: 40 * 24 * time.Hour,
	}}
	// Plan 时不占用
	intents, err := svc.Plan(cands, map[string]retention.RunGuard{
		"run_race": {Terminal: true},
	})
	if err != nil || len(intents) != 1 {
		t.Fatalf("plan: %v", err)
	}
	// Execute 时已占用 → 拒绝
	_, err = svc.Execute(intents[0].IntentID)
	if !errors.Is(err, retention.ErrProtected) {
		t.Fatalf("want ErrProtected, got %v", err)
	}
	pres, _, _ := svc.Query(retention.TargetRunDir, target, retention.RunGuard{Occupies: true})
	if pres != retention.PresenceProtected && pres != retention.PresenceFailed {
		t.Fatalf("presence=%s", pres)
	}

	occupied = false
	fails := 1
	svc.Remove = func(path string) error {
		if fails > 0 {
			fails--
			return errors.New("injected remove failure")
		}
		return os.RemoveAll(path)
	}
	_, err = svc.Execute(intents[0].IntentID)
	if err == nil {
		t.Fatal("first retry should fail injection")
	}
	in, err := svc.Execute(intents[0].IntentID)
	if err != nil || in.State != retention.IntentDeleted {
		t.Fatalf("second retry: %v %#v", err, in)
	}
}

func TestRefuseOutsideRootAndPendingJournal(t *testing.T) {
	root := t.TempDir()
	ledger, _ := retention.OpenLedger(filepath.Join(root, "ledger"))
	svc := &retention.Service{Policy: retention.DefaultPolicy(), Ledger: ledger, Root: root}

	outside := filepath.Join(t.TempDir(), "user-home-file")
	_ = os.WriteFile(outside, []byte("x"), 0o644)
	id := ""
	{
		intents, err := svc.Plan([]retention.Candidate{{
			Kind: retention.TargetLog, Path: outside, RunID: "r", Age: 40 * 24 * time.Hour,
		}}, map[string]retention.RunGuard{"r": {Terminal: true}})
		if err != nil || len(intents) != 1 {
			t.Fatalf("plan outside still records intent: %v", err)
		}
		id = intents[0].IntentID
	}
	_, err := svc.Execute(id)
	if !errors.Is(err, retention.ErrOutsideRoot) {
		t.Fatalf("want ErrOutsideRoot, got %v", err)
	}

	d := retention.Evaluate(retention.DefaultPolicy(), retention.Candidate{
		Kind: retention.TargetJournal, Path: filepath.Join(root, "j"), RunID: "r", Age: 40 * 24 * time.Hour,
	}, retention.RunGuard{PendingJournal: 2, Terminal: true})
	if d.Action != retention.ActionRetain {
		t.Fatal("pending journal must retain")
	}
}

func TestProtectedMissingIsNotCleaned(t *testing.T) {
	root := t.TempDir()
	ledger, _ := retention.OpenLedger(filepath.Join(root, "ledger"))
	svc := &retention.Service{Policy: retention.DefaultPolicy(), Ledger: ledger, Root: root}
	missing := filepath.Join(root, "runs", "gone", "stdout.log")
	pres, _, err := svc.Query(retention.TargetLog, missing, retention.RunGuard{
		Occupies: true, ReconciliationUnknown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pres != retention.PresenceMissing {
		t.Fatalf("protected but absent path is missing (缺测), got %s", pres)
	}
}
