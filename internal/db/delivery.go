package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// 交付清单与验证证据（契约 maw.delivery.manifest/v1）。
//
// 两条落库规则：
//
//  1. 交付必须能追到确定的 Run 与任务版本：写入时校验 run.InstanceID/VersionNo 与
//     交付一致，否则拒绝（E312／E330）。
//  2. 验证与评审绑定**当时的交付指纹**：指纹覆盖基线／候选提交与全部产物引用
//     （身份、哈希、是否存在、是否在写盘范围内）。产物或提交一变，旧验证自动失效，
//     不需要谁记得去撤销。
//
// DeliveryRecord 是一条交付及其派生状态。
type DeliveryRecord struct {
	Delivery       model.Delivery `json:"delivery"`
	ProjectID      string         `json:"project_id"`
	Fingerprint    string         `json:"fingerprint"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

// CompletionReadiness 是「能不能判完成」的三项证据状态；只陈述事实，不替人下结论。
type CompletionReadiness struct {
	PassingVerification bool `json:"passing_verification"`
	ApprovedReview      bool `json:"approved_review"`
	Merged              bool `json:"merged"`
	MergeRequired       bool `json:"merge_required"`
	StaleVerifications  int  `json:"stale_verifications"`
	ReviewStale         bool `json:"review_stale"`
}

// Ready 报告现有证据是否足以支撑完成判定。
func (r CompletionReadiness) Ready() bool {
	if !r.PassingVerification || !r.ApprovedReview {
		return false
	}
	if r.MergeRequired && !r.Merged {
		return false
	}
	return true
}

// DeliveryFingerprint 把「这次交付到底交付了什么」压成摘要。
//
// 覆盖基线/候选提交与全部产物引用，产物按身份排序以保证同一份清单得到同一指纹。
func DeliveryFingerprint(d model.Delivery) (string, error) {
	type artifact struct {
		ID      string  `json:"id"`
		Kind    string  `json:"kind"`
		Ref     string  `json:"ref"`
		Exists  bool    `json:"exists"`
		InScope bool    `json:"in_scope"`
		SHA256  *string `json:"sha256,omitempty"`
	}
	items := make([]artifact, 0, len(d.Artifacts))
	for _, a := range d.Artifacts {
		item := artifact{
			ID: string(a.ArtifactID), Kind: string(a.Kind), Ref: a.Ref,
			Exists: a.Exists, InScope: a.InWriteScope,
		}
		if a.SHA256 != nil {
			h := string(*a.SHA256)
			item.SHA256 = &h
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	payload := struct {
		Instance  string     `json:"instance"`
		Version   int        `json:"version"`
		Baseline  string     `json:"baseline"`
		Candidate *string    `json:"candidate,omitempty"`
		Artifacts []artifact `json:"artifacts"`
	}{
		Instance: string(d.TaskInstance), Version: int(d.TaskVersion),
		Baseline: string(d.BaselineCommit), Artifacts: items,
	}
	if d.CandidateCommit != nil {
		c := string(*d.CandidateCommit)
		payload.Candidate = &c
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// SaveDelivery 写入一次交付；同一幂等键同摘要重放返回原记录，不同摘要报 E252。
func SaveDelivery(tx *sql.Tx, projectID string, d model.Delivery, idempotencyKey string) (*DeliveryRecord, bool, error) {
	if strings.TrimSpace(string(d.DeliveryID)) == "" {
		return nil, false, model.Violation(model.CodeDeliveryFieldInvalid, "delivery.delivery_id", "交付必须有身份")
	}
	if strings.TrimSpace(string(d.TaskInstance)) == "" || d.TaskVersion < 1 || strings.TrimSpace(string(d.RunID)) == "" {
		return nil, false, model.Violation(model.CodeDeliveryTaskMissing, "delivery",
			"交付必须绑定任务实例、版本与 Run")
	}
	if d.Mode != model.DeliveryCode && d.Mode != model.DeliveryNonCode {
		return nil, false, model.Violationf(model.CodeDeliveryFieldInvalid, "delivery.mode",
			"只能是 code/non_code，收到 %q", d.Mode)
	}
	if strings.TrimSpace(string(d.BaselineCommit)) == "" {
		return nil, false, model.Violation(model.CodeCommitRefInvalid, "delivery.baseline_commit", "必须记录基线提交")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, false, model.Violation(model.CodeClaimWithoutGuards, "delivery.idempotency_key",
			"交付必须带幂等键")
	}

	// 交付必须追到确定的 Run 与版本（E312／E330）。
	run, err := GetRun(tx, string(d.RunID))
	if err != nil {
		return nil, false, err
	}
	if run == nil {
		return nil, false, model.Violationf(model.CodeDeliveryRunMissing, "delivery.run_id",
			"Run %q 不存在，交付无法追溯", d.RunID)
	}
	if run.InstanceID != string(d.TaskInstance) {
		return nil, false, model.Violationf(model.CodeDeliveryRunMissing, "delivery.task_instance",
			"Run %q 属于实例 %s，不是 %s", d.RunID, run.InstanceID, d.TaskInstance)
	}
	if run.VersionNo != d.TaskVersion {
		return nil, false, model.Violationf(model.CodeVerificationVersion, "delivery.task_version",
			"Run %q 冻结的是 v%d，交付声明 v%d", d.RunID, run.VersionNo, d.TaskVersion)
	}

	fingerprint, err := DeliveryFingerprint(d)
	if err != nil {
		return nil, false, err
	}
	digest := RequestDigest(projectID, string(d.DeliveryID), string(d.TaskInstance),
		strconv.Itoa(int(d.TaskVersion)), string(d.RunID), fingerprint, idempotencyKey)

	existing, err := findDeliveryByKey(tx, projectID, idempotencyKey)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		if existing.Fingerprint != fingerprint {
			return nil, false, model.Violationf(model.CodeIdempotencyConflict, "delivery.idempotency_key",
				"同键不同内容：已登记指纹 %s，本次 %s", existing.Fingerprint, fingerprint)
		}
		return existing, true, nil
	}

	manifest, err := json.Marshal(d)
	if err != nil {
		return nil, false, err
	}
	now := UTCNow()
	_, err = tx.Exec(`INSERT INTO deliveries(delivery_id, project_id, instance_id, version_no, run_id,
  mode, baseline_commit, candidate_commit, delivered_at, manifest_json, fingerprint,
  supersedes, superseded_by, idempotency_key, request_digest, created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(d.DeliveryID), projectID, string(d.TaskInstance), int(d.TaskVersion), string(d.RunID),
		string(d.Mode), string(d.BaselineCommit), nullStr(commitOrEmpty(d.CandidateCommit)),
		formatTS(d.DeliveredAt), string(manifest), fingerprint,
		nullStr(deliveryIDOrEmpty(d.Supersedes)), nullStr(deliveryIDOrEmpty(d.SupersededBy)),
		idempotencyKey, digest, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, false, model.Violationf(model.CodeIdempotencyConflict, "delivery.delivery_id",
				"交付 %q 已被并发写入", d.DeliveryID)
		}
		return nil, false, err
	}
	rec, err := GetDelivery(tx, string(d.DeliveryID))
	return rec, false, err
}

// UpdateDeliveryManifest 在产物或提交被核实变化后刷新清单。
//
// 指纹随之变化，此前所有验证与评审自动变为过期——这不是撤销历史，而是让
// 「旧证据不能用来完成新结果」成为可计算的判断。
func UpdateDeliveryManifest(tx *sql.Tx, d model.Delivery) (*DeliveryRecord, error) {
	existing, err := GetDelivery(tx, string(d.DeliveryID))
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, model.Violationf(model.CodeDeliveryTaskMissing, "delivery.delivery_id",
			"%q 不存在，不能刷新清单", d.DeliveryID)
	}
	if string(d.TaskInstance) != string(existing.Delivery.TaskInstance) || d.TaskVersion != existing.Delivery.TaskVersion ||
		string(d.RunID) != string(existing.Delivery.RunID) {
		return nil, model.Violation(model.CodeDeliveryFieldInvalid, "delivery",
			"刷新清单不得改变实例、版本或 Run（那是一次新交付）")
	}
	fingerprint, err := DeliveryFingerprint(d)
	if err != nil {
		return nil, err
	}
	manifest, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(`UPDATE deliveries SET manifest_json = ?, fingerprint = ?,
  candidate_commit = ? WHERE delivery_id = ?`,
		string(manifest), fingerprint, nullStr(commitOrEmpty(d.CandidateCommit)), string(d.DeliveryID))
	if err != nil {
		return nil, err
	}
	return GetDelivery(tx, string(d.DeliveryID))
}

// AddVerification 追加一条验证结论，绑定当前交付指纹与被测对象。
//
// 三类拒绝：目标版本不是本次交付的版本（E330）；目标提交／产物哈希与当前交付不符
// （E331／E334）；只有退出码没有任何结果证据却声明通过（E333）。
func AddVerification(tx *sql.Tx, deliveryID string, v model.Verification) error {
	rec, err := GetDelivery(tx, deliveryID)
	if err != nil {
		return err
	}
	if rec == nil {
		return model.Violationf(model.CodeDeliveryTaskMissing, "verification.delivery_id",
			"交付 %q 不存在", deliveryID)
	}
	if strings.TrimSpace(string(v.VerificationID)) == "" {
		return model.Violation(model.CodeDeliveryFieldInvalid, "verification.verification_id", "验证必须有身份")
	}
	if v.TargetTaskVersion != rec.Delivery.TaskVersion {
		return model.Violationf(model.CodeVerificationVersion, "verification.target_task_version",
			"必须等于交付版本 v%d，收到 v%d", rec.Delivery.TaskVersion, v.TargetTaskVersion)
	}
	if v.TargetCommit != nil && rec.Delivery.CandidateCommit != nil &&
		*v.TargetCommit != *rec.Delivery.CandidateCommit {
		return model.Violationf(model.CodeVerificationStaleCommit, "verification.target_commit",
			"被测提交 %s 不是当前候选提交 %s", *v.TargetCommit, *rec.Delivery.CandidateCommit)
	}
	if v.TargetArtifactID != nil {
		artifact := rec.Delivery.Artifact(*v.TargetArtifactID)
		if artifact == nil {
			return model.Violationf(model.CodeArtifactMissing, "verification.target_artifact_id",
				"产物 %q 不在本次交付清单内", *v.TargetArtifactID)
		}
		if v.TargetArtifactSHA256 != nil && artifact.SHA256 != nil && *v.TargetArtifactSHA256 != *artifact.SHA256 {
			return model.Violationf(model.CodeVerificationArtifact, "verification.target_artifact_sha256",
				"产物 %q 已变为 %s", *v.TargetArtifactID, *artifact.SHA256)
		}
	}
	if v.Result == model.VerifyPass && !v.HasResultEvidence() {
		return model.Violation(model.CodeVerificationExitOnly, "verification.evidence",
			"只有退出码不算通过，必须给出结果类证据")
	}
	manifest, err := json.Marshal(v)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO delivery_verifications(verification_id, delivery_id, result,
  target_version, target_commit, target_artifact_id, target_artifact_sha256,
  fingerprint, ran_at, verification_json)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(verification_id) DO NOTHING`,
		string(v.VerificationID), deliveryID, string(v.Result), int(v.TargetTaskVersion),
		nullStr(commitOrEmpty(v.TargetCommit)), nullStr(artifactIDOrEmpty(v.TargetArtifactID)),
		nullStr(shaOrEmpty(v.TargetArtifactSHA256)), rec.Fingerprint, formatTS(v.RanAt), string(manifest))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var same int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM delivery_verifications
WHERE verification_id = ? AND delivery_id = ? AND verification_json = ?`,
			string(v.VerificationID), deliveryID, string(manifest)).Scan(&same); err != nil {
			return err
		}
		if same == 0 {
			return model.Violationf(model.CodeIdempotencyConflict, "verification.verification_id",
				"验证 %q 已存在且内容不同", v.VerificationID)
		}
		return nil
	}
	return nil
}

// AddReview 记录人工结论，同样绑定当前交付指纹。
func AddReview(tx *sql.Tx, deliveryID string, r model.Review) error {
	rec, err := GetDelivery(tx, deliveryID)
	if err != nil {
		return err
	}
	if rec == nil {
		return model.Violationf(model.CodeDeliveryTaskMissing, "review.delivery_id", "交付 %q 不存在", deliveryID)
	}
	if !r.Verdict.Valid() {
		return model.Violationf(model.CodeDeliveryFieldInvalid, "review.verdict", "未知结论 %q", r.Verdict)
	}
	if !r.Required && r.Verdict != model.VerdictNotRequired {
		return model.Violation(model.CodeDeliveryFieldInvalid, "review.verdict",
			"不需要评审时结论必须是 not_required")
	}
	if r.Required && r.Verdict == model.VerdictNotRequired {
		return model.Violation(model.CodeDeliveryFieldInvalid, "review.verdict",
			"要求评审时不能填 not_required")
	}
	manifest, err := json.Marshal(r)
	if err != nil {
		return err
	}
	var reviewedAt any
	if r.ReviewedAt != nil {
		reviewedAt = formatTS(*r.ReviewedAt)
	}
	_, err = tx.Exec(`INSERT INTO delivery_reviews(delivery_id, required, verdict, reviewer,
  reviewed_at, fingerprint, review_json) VALUES (?,?,?,?,?,?,?)
ON CONFLICT(delivery_id) DO UPDATE SET required=excluded.required, verdict=excluded.verdict,
  reviewer=excluded.reviewer, reviewed_at=excluded.reviewed_at,
  fingerprint=excluded.fingerprint, review_json=excluded.review_json`,
		deliveryID, boolInt(r.Required), string(r.Verdict), r.Reviewer, reviewedAt,
		rec.Fingerprint, string(manifest))
	return err
}

// DeliveryReadiness 汇总完成判定所需的证据；过期证据不算数。
func DeliveryReadiness(tx *sql.Tx, deliveryID string) (CompletionReadiness, error) {
	rec, err := GetDelivery(tx, deliveryID)
	if err != nil {
		return CompletionReadiness{}, err
	}
	if rec == nil {
		return CompletionReadiness{}, model.Violationf(model.CodeDeliveryTaskMissing, "delivery.delivery_id",
			"%q 不存在", deliveryID)
	}
	out := CompletionReadiness{
		MergeRequired: rec.Delivery.Merge.Required,
		Merged:        rec.Delivery.Merge.Merged,
	}
	var passing, stale int
	// pass 是自动验证通过，manual 是人工验收通过；两者都算「有通过结论」。
	if err := tx.QueryRow(`SELECT
  COALESCE(SUM(CASE WHEN result IN (?, ?) AND fingerprint = ? THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN fingerprint <> ? THEN 1 ELSE 0 END), 0)
FROM delivery_verifications WHERE delivery_id = ?`,
		string(model.VerifyPass), string(model.VerifyManual), rec.Fingerprint,
		rec.Fingerprint, deliveryID).
		Scan(&passing, &stale); err != nil {
		return out, err
	}
	out.PassingVerification = passing > 0
	out.StaleVerifications = stale

	var verdict, fingerprint string
	var required int
	err = tx.QueryRow(`SELECT required, verdict, fingerprint FROM delivery_reviews WHERE delivery_id = ?`,
		deliveryID).Scan(&required, &verdict, &fingerprint)
	switch {
	case err == sql.ErrNoRows:
		out.ApprovedReview = !rec.Delivery.Review.Required
	case err != nil:
		return out, err
	default:
		out.ReviewStale = fingerprint != rec.Fingerprint
		out.ApprovedReview = fingerprint == rec.Fingerprint && model.Verdict(verdict) == model.VerdictApproved
	}
	return out, nil
}

// GetDelivery 返回交付记录；不存在返回 nil。
func GetDelivery(tx *sql.Tx, deliveryID string) (*DeliveryRecord, error) {
	row := tx.QueryRow(`SELECT delivery_id, project_id, manifest_json, fingerprint,
  idempotency_key, created_at FROM deliveries WHERE delivery_id = ?`, deliveryID)
	return scanDelivery(row)
}

// ListDeliveries 返回某实例的交付历史（可按工程/实例筛选，空表示不限）。
func ListDeliveries(tx *sql.Tx, projectID, instanceID string) ([]*DeliveryRecord, error) {
	q := `SELECT delivery_id, project_id, manifest_json, fingerprint, idempotency_key, created_at
FROM deliveries WHERE 1=1`
	var args []any
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}
	if instanceID != "" {
		q += ` AND instance_id = ?`
		args = append(args, instanceID)
	}
	q += ` ORDER BY delivered_at ASC, delivery_id ASC`
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DeliveryRecord
	for rows.Next() {
		rec, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeliveryForRun 返回某 Run 的交付；一次 Run 至多一条交付。
func DeliveryForRun(tx *sql.Tx, runID string) (*DeliveryRecord, error) {
	row := tx.QueryRow(`SELECT delivery_id, project_id, manifest_json, fingerprint,
  idempotency_key, created_at FROM deliveries WHERE run_id = ? ORDER BY created_at DESC LIMIT 1`, runID)
	return scanDelivery(row)
}

func findDeliveryByKey(tx *sql.Tx, projectID, key string) (*DeliveryRecord, error) {
	row := tx.QueryRow(`SELECT delivery_id, project_id, manifest_json, fingerprint,
  idempotency_key, created_at FROM deliveries WHERE project_id = ? AND idempotency_key = ?`,
		projectID, key)
	return scanDelivery(row)
}

func scanDelivery(row interface{ Scan(dest ...any) error }) (*DeliveryRecord, error) {
	var (
		rec      DeliveryRecord
		id       string
		manifest string
		idemKey  sql.NullString
	)
	err := row.Scan(&id, &rec.ProjectID, &manifest, &rec.Fingerprint, &idemKey, &rec.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(manifest), &rec.Delivery); err != nil {
		return nil, err
	}
	rec.IdempotencyKey = idemKey.String
	return &rec, nil
}

func commitOrEmpty(c *model.Commit) string {
	if c == nil {
		return ""
	}
	return string(*c)
}

func deliveryIDOrEmpty(c *model.DeliveryID) string {
	if c == nil {
		return ""
	}
	return string(*c)
}

func artifactIDOrEmpty(a *model.ArtifactID) string {
	if a == nil {
		return ""
	}
	return string(*a)
}

func shaOrEmpty(s *model.SHA256) string {
	if s == nil {
		return ""
	}
	return string(*s)
}
