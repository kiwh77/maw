package db

import (
	"database/sql"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

type deliveryFixture struct {
	instanceID string
	runID      string
	version    model.VersionNo
}

// seedDeliveryFixture 造一个已完成的 Run（v1），交付必须能追到它。
func seedDeliveryFixture(t *testing.T, path string) deliveryFixture {
	t.Helper()
	f := deliveryFixture{version: 1}
	mustWithTx(t, path, func(tx *sql.Tx) error {
		inst, err := CreateInstance(tx, testProject, "VN-700", InstanceDraft{Title: "交付"})
		if err != nil {
			return err
		}
		f.instanceID = string(inst.InstanceID)
		runID := "run_delivery_1"
		f.runID = runID
		return createRun(tx, &Run{
			ID: runID, ProjectID: testProject, InstanceID: f.instanceID,
			VersionNo: f.version, Generation: 1, Phase: model.PhaseCompleted,
			Reconciliation: model.ReconciliationConfirmed, DesiredAction: model.ActionNone,
			ProcessStopConfirmed: true, DeliveryFrozen: true,
			PhaseHistory: []model.PhaseEntry{{Phase: model.PhaseReserved, At: time.Now().UTC()}},
			Evidence:     []string{"exit 0"}, CreatedAt: UTCNow(), UpdatedAt: UTCNow(),
		})
	})
	return f
}

func sha(s string) *model.SHA256 {
	v := model.SHA256(s)
	return &v
}

func commit(s string) *model.Commit {
	v := model.Commit(s)
	return &v
}

func sampleDelivery(f deliveryFixture, candidate string) model.Delivery {
	return model.Delivery{
		DeliveryID:      "dlv_0001",
		TaskInstance:    model.InstanceID(f.instanceID),
		TaskVersion:     f.version,
		RunID:           model.RunID(f.runID),
		Mode:            model.DeliveryCode,
		BaselineCommit:  model.Commit("aaaaaaaa"),
		CandidateCommit: commit(candidate),
		DeliveredAt:     time.Now().UTC(),
		Artifacts: []model.Artifact{{
			ArtifactID: "art_001", Kind: model.ArtifactCommit, Ref: candidate,
			DeclaredBy: model.DeclaredByCLI, Exists: true, InWriteScope: true,
			SHA256: sha("1111111111111111111111111111111111111111111111111111111111111111"),
		}},
		Review: model.Review{Required: true},
		Merge:  model.Merge{Required: true, Merged: true},
	}
}

func TestSaveDeliveryTracesRunAndVersion(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	f := seedDeliveryFixture(t, path)

	mustWithTx(t, path, func(tx *sql.Tx) error {
		rec, replay, err := SaveDelivery(tx, testProject, sampleDelivery(f, "bbbbbbbb"), "dlv-key-1")
		if err != nil {
			return err
		}
		if replay {
			t.Fatal("首次写入不该是重放")
		}
		if rec.Delivery.RunID != model.RunID(f.runID) || rec.Delivery.TaskVersion != f.version {
			t.Fatalf("交付未绑定 Run/版本: %+v", rec.Delivery)
		}
		byRun, err := DeliveryForRun(tx, f.runID)
		if err != nil {
			return err
		}
		if byRun == nil || string(byRun.Delivery.DeliveryID) != "dlv_0001" {
			t.Fatalf("按 Run 追不到交付: %+v", byRun)
		}
		list, err := ListDeliveries(tx, testProject, f.instanceID)
		if err != nil || len(list) != 1 {
			t.Fatalf("交付历史 %v %v", list, err)
		}
		return nil
	})

	// 版本对不上：拒绝（E330）。
	bad := sampleDelivery(f, "bbbbbbbb")
	bad.TaskVersion = 2
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, _, err := SaveDelivery(tx, testProject, bad, "dlv-key-2")
		return err
	}); !hasCode(err, model.CodeVerificationVersion) {
		t.Fatalf("版本不符应报 E330，得到 %v", err)
	}

	// Run 属于别的实例：拒绝（E312）。
	bad = sampleDelivery(f, "bbbbbbbb")
	bad.TaskInstance = "ti_otherone"
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, _, err := SaveDelivery(tx, testProject, bad, "dlv-key-3")
		return err
	}); !hasCode(err, model.CodeDeliveryRunMissing) {
		t.Fatalf("Run 归属不符应报 E312，得到 %v", err)
	}
}

func TestSaveDeliveryIsIdempotent(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	f := seedDeliveryFixture(t, path)
	d := sampleDelivery(f, "bbbbbbbb")

	mustWithTx(t, path, func(tx *sql.Tx) error {
		_, _, err := SaveDelivery(tx, testProject, d, "dlv-key-1")
		return err
	})
	mustWithTx(t, path, func(tx *sql.Tx) error {
		rec, replay, err := SaveDelivery(tx, testProject, d, "dlv-key-1")
		if err != nil {
			return err
		}
		if !replay {
			t.Fatal("同键同内容应重放")
		}
		if string(rec.Delivery.DeliveryID) != "dlv_0001" {
			t.Fatalf("重放返回了别的交付: %+v", rec.Delivery)
		}
		return nil
	})

	// 同键不同内容 → E252。
	other := sampleDelivery(f, "cccccccc")
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, _, err := SaveDelivery(tx, testProject, other, "dlv-key-1")
		return err
	}); !hasCode(err, model.CodeIdempotencyConflict) {
		t.Fatalf("同键不同内容应报 E252，得到 %v", err)
	}
}

func TestVerificationInvalidatedByArtifactOrCommitChange(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	f := seedDeliveryFixture(t, path)
	d := sampleDelivery(f, "bbbbbbbb")

	mustWithTx(t, path, func(tx *sql.Tx) error {
		if _, _, err := SaveDelivery(tx, testProject, d, "dlv-key-1"); err != nil {
			return err
		}
		// 只有退出码的「通过」不算通过（E333）。
		exitOnly := model.Verification{
			VerificationID: "vfy_exit", Command: "go test", TargetTaskVersion: f.version,
			Result: model.VerifyPass, Evidence: []string{"exit_code:0"}, RanAt: time.Now().UTC(),
		}
		if err := AddVerification(tx, "dlv_0001", exitOnly); !hasCode(err, model.CodeVerificationExitOnly) {
			t.Fatalf("只有退出码应报 E333，得到 %v", err)
		}
		good := model.Verification{
			VerificationID: "vfy_001", Command: "go test ./...", TargetTaskVersion: f.version,
			TargetCommit: commit("bbbbbbbb"),
			Result:       model.VerifyPass, Evidence: []string{"ok github.com/kiwh77/maw"}, RanAt: time.Now().UTC(),
		}
		if err := AddVerification(tx, "dlv_0001", good); err != nil {
			return err
		}
		// 同一条重放幂等；内容不同则冲突。
		if err := AddVerification(tx, "dlv_0001", good); err != nil {
			t.Fatalf("同内容重放应幂等: %v", err)
		}
		changed := good
		changed.Evidence = []string{"别的证据"}
		if err := AddVerification(tx, "dlv_0001", changed); !hasCode(err, model.CodeIdempotencyConflict) {
			t.Fatalf("同身份不同内容应冲突，得到 %v", err)
		}
		return nil
	})

	if err := WithReadTx(path, func(tx *sql.Tx) error {
		ready, err := DeliveryReadiness(tx, "dlv_0001")
		if err != nil {
			return err
		}
		if !ready.PassingVerification || ready.StaleVerifications != 0 {
			t.Fatalf("验证应有效: %+v", ready)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 产物变化：清单刷新后旧验证失效。
	updated := sampleDelivery(f, "bbbbbbbb")
	updated.Artifacts[0].SHA256 = sha("2222222222222222222222222222222222222222222222222222222222222222")
	mustWithTx(t, path, func(tx *sql.Tx) error {
		_, err := UpdateDeliveryManifest(tx, updated)
		return err
	})
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		ready, err := DeliveryReadiness(tx, "dlv_0001")
		if err != nil {
			return err
		}
		if ready.PassingVerification {
			t.Fatal("产物变了，旧验证不该还能支撑完成")
		}
		if ready.StaleVerifications != 1 {
			t.Fatalf("应记 1 条过期验证，得到 %d", ready.StaleVerifications)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 针对旧提交／旧产物哈希的验证直接被拒。
	if err := WithTx(path, func(tx *sql.Tx) error {
		async := model.Verification{
			VerificationID: "vfy_stale", TargetTaskVersion: f.version,
			TargetCommit: commit("99999999"), Result: model.VerifyPass,
			Evidence: []string{"ok"}, RanAt: time.Now().UTC(),
		}
		return AddVerification(tx, "dlv_0001", async)
	}); !hasCode(err, model.CodeVerificationStaleCommit) {
		t.Fatalf("旧提交应报 E331，得到 %v", err)
	}
	if err := WithTx(path, func(tx *sql.Tx) error {
		artifactID := model.ArtifactID("art_001")
		oldSHA := model.SHA256("1111111111111111111111111111111111111111111111111111111111111111")
		old := model.Verification{
			VerificationID: "vfy_oldsha", TargetTaskVersion: f.version,
			TargetCommit: commit("bbbbbbbb"), TargetArtifactID: &artifactID, TargetArtifactSHA256: &oldSHA,
			Result: model.VerifyPass, Evidence: []string{"ok"}, RanAt: time.Now().UTC(),
		}
		return AddVerification(tx, "dlv_0001", old)
	}); !hasCode(err, model.CodeVerificationArtifact) {
		t.Fatalf("旧产物哈希应报 E334，得到 %v", err)
	}
}

func TestReviewBindsFingerprintAndReadiness(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	f := seedDeliveryFixture(t, path)
	d := sampleDelivery(f, "bbbbbbbb")
	d.Merge.Merged = false // 还没合入：即使有验证与评审也不能判完成

	mustWithTx(t, path, func(tx *sql.Tx) error {
		if _, _, err := SaveDelivery(tx, testProject, d, "dlv-key-1"); err != nil {
			return err
		}
		if err := AddVerification(tx, "dlv_0001", model.Verification{
			VerificationID: "vfy_001", TargetTaskVersion: f.version,
			Result: model.VerifyPass, Evidence: []string{"ok"}, RanAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		return AddReview(tx, "dlv_0001", model.Review{
			Required: true, Verdict: model.VerdictApproved, Reviewer: "reviewer-1",
		})
	})

	if err := WithReadTx(path, func(tx *sql.Tx) error {
		ready, err := DeliveryReadiness(tx, "dlv_0001")
		if err != nil {
			return err
		}
		if !ready.PassingVerification || !ready.ApprovedReview {
			t.Fatalf("证据应齐: %+v", ready)
		}
		if ready.Ready() {
			t.Fatal("尚未合入不应判完成")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 评审结论与 required 必须一致。
	if err := WithTx(path, func(tx *sql.Tx) error {
		return AddReview(tx, "dlv_0001", model.Review{Required: true, Verdict: model.VerdictNotRequired})
	}); err == nil {
		t.Fatal("要求评审却填 not_required 应被拒")
	}

	// 候选提交变了：评审随指纹一起过期，只有重新评审才能再判完成。
	updated := sampleDelivery(f, "cccccccc")
	updated.Merge.Merged = true
	mustWithTx(t, path, func(tx *sql.Tx) error {
		_, err := UpdateDeliveryManifest(tx, updated)
		return err
	})
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		ready, err := DeliveryReadiness(tx, "dlv_0001")
		if err != nil {
			return err
		}
		if ready.ApprovedReview || !ready.ReviewStale {
			t.Fatalf("提交变了，旧评审应过期: %+v", ready)
		}
		if ready.Ready() {
			t.Fatal("过期评审不能支撑完成")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 新版候选提交要重新验证 + 重新评审（合并事实已为 true）才能判完成。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		if err := AddVerification(tx, "dlv_0001", model.Verification{
			VerificationID: "vfy_002", TargetTaskVersion: f.version, TargetCommit: commit("cccccccc"),
			Result: model.VerifyPass, Evidence: []string{"ok after change"}, RanAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		return AddReview(tx, "dlv_0001", model.Review{
			Required: true, Verdict: model.VerdictApproved, Reviewer: "reviewer-2",
		})
	})
	if err := WithTx(path, func(tx *sql.Tx) error {
		ready, err := DeliveryReadiness(tx, "dlv_0001")
		if err != nil {
			return err
		}
		if !ready.Ready() {
			t.Fatalf("应可判完成: %+v", ready)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNonCodeDeliveryNeedsManualAcceptance(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	f := seedDeliveryFixture(t, path)
	d := sampleDelivery(f, "bbbbbbbb")
	d.Mode = model.DeliveryNonCode
	d.Review = model.Review{Required: false, Verdict: model.VerdictNotRequired}
	d.Merge = model.Merge{Required: false, Merged: false}

	mustWithTx(t, path, func(tx *sql.Tx) error {
		if _, _, err := SaveDelivery(tx, testProject, d, "dlv-key-1"); err != nil {
			return err
		}
		return AddVerification(tx, "dlv_0001", model.Verification{
			VerificationID: "vfy_manual", TargetTaskVersion: f.version,
			Result: model.VerifyManual, Evidence: []string{"人工确认文档已更新"},
			RanAt: time.Now().UTC(),
		})
	})
	if err := WithTx(path, func(tx *sql.Tx) error {
		ready, err := DeliveryReadiness(tx, "dlv_0001")
		if err != nil {
			return err
		}
		if !ready.Ready() {
			t.Fatalf("非代码任务人工验收后应可判完成: %+v", ready)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
