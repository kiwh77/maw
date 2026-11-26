package delivery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/home"
)

const testProject = model.ProjectID("716c30878ba1b95f")

var fixedNow = time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)

// repoFixture 是一个真实的小仓库：main 上有基线提交，候选提交改了 write_scope 内的文件。
type repoFixture struct {
	repo     string
	baseline model.Commit
	cand     model.Commit
	worktree string
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := home.Git(dir, args...)
	if err != nil {
		t.Fatalf("git %v 失败：%v（%s）", args, err, out)
	}
	return out
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
}

func newRepoFixture(t *testing.T) repoFixture {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init", "-b", "main")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "test")
	gitRun(t, repo, "config", "commit.gpgsign", "false")

	writeFile(t, repo, "internal/x/a.go", "package x\n\n// baseline\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "baseline")
	baseline := model.Commit(gitRun(t, repo, "rev-parse", "HEAD"))

	writeFile(t, repo, "internal/x/a.go", "package x\n\n// candidate\n")
	writeFile(t, repo, "internal/x/b.go", "package x\n\nfunc B() {}\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "candidate")
	cand := model.Commit(gitRun(t, repo, "rev-parse", "HEAD"))

	return repoFixture{repo: repo, baseline: baseline, cand: cand, worktree: repo}
}

func newCollector(t *testing.T, f repoFixture, store Store, opts ...Option) *Collector {
	t.Helper()
	c, err := New(testProject, f.repo, []string{"internal/x/**"}, store, opts...)
	if err != nil {
		t.Fatalf("创建收取器失败：%v", err)
	}
	return c
}

// candidate 造一份「产物齐全、但还没验证／评审／合入」的候选。
func (f repoFixture) candidate() Candidate {
	commit := f.cand
	return Candidate{
		TaskInstance:    "ti_vn502a",
		TaskVersion:     1,
		RunID:           "run_a001",
		Mode:            model.DeliveryCode,
		BaselineCommit:  f.baseline,
		CandidateCommit: &commit,
		Worktree:        f.worktree,
		RunOutcome:      model.OutcomeCompleted,
		Artifacts: []model.ArtifactCandidate{
			{Kind: model.ArtifactCommit, Ref: string(f.cand), DeclaredBy: model.DeclaredByCLI},
			{Kind: model.ArtifactFile, Ref: "internal/x/a.go", DeclaredBy: model.DeclaredByCLI},
		},
	}
}

func hasCode(vs []*model.Error, code model.Code) bool { return model.HasCode(vs, code) }

func asViolations(t *testing.T, err error) Violations {
	t.Helper()
	if err == nil {
		return nil
	}
	v, ok := err.(Violations)
	if !ok {
		t.Fatalf("期望 Violations，实际 %T：%v", err, err)
	}
	return v
}

// TestCollectFreezesVerifiedFacts 覆盖主路径：产物逐条核实，判定 pending（还缺验证与合入）。
func TestCollectFreezesVerifiedFacts(t *testing.T) {
	f := newRepoFixture(t)
	c := newCollector(t, f, nil, WithClock(func() time.Time { return fixedNow }))

	d, err := c.Collect(f.candidate())
	if err != nil {
		t.Fatalf("产物齐全的候选不应报错：%v", err)
	}
	if len(d.Artifacts) != 2 {
		t.Fatalf("应冻结两条产物，实际 %d", len(d.Artifacts))
	}

	commitArt, fileArt := d.Artifacts[0], d.Artifacts[1]
	if !commitArt.Exists || !commitArt.InWriteScope {
		t.Fatalf("候选提交应存在且在范围内，实际 %+v", commitArt)
	}
	if !fileArt.Exists || !fileArt.InWriteScope {
		t.Fatalf("文件应存在且在范围内，实际 %+v", fileArt)
	}
	if fileArt.SHA256 == nil || !fileArt.SHA256.Valid() {
		t.Fatal("文件产物必须由 maw 侧算内容哈希")
	}
	if fileArt.DeclaredBy != model.DeclaredByCLI {
		t.Fatal("CLI 声明应保持为候选来源，不得升级")
	}
	if d.DeliveryID == "" || !strings.HasPrefix(string(d.DeliveryID), "dlv_") {
		t.Fatalf("交付身份不合法：%q", d.DeliveryID)
	}
	if d.Completion.Decision != model.DecisionPending {
		t.Fatalf("缺验证与合入时只能是 pending，实际 %s", d.Completion.Decision)
	}
	if d.Completion.Reason == "" {
		t.Fatal("pending 必须写清缺什么")
	}
	if d.DeliveredAt != fixedNow {
		t.Fatalf("冻结时间应取注入时钟，实际 %s", d.DeliveredAt)
	}
}

// TestCollectRejectsOutOfScopeCommit 覆盖越界改动（E321）。
func TestCollectRejectsOutOfScopeCommit(t *testing.T) {
	f := newRepoFixture(t)
	// 候选提交里再改一个范围外的文件。
	writeFile(t, f.repo, "docs/out.md", "out of scope\n")
	gitRun(t, f.repo, "add", ".")
	gitRun(t, f.repo, "commit", "-m", "out of scope")
	cand := f.candidate()
	commit := model.Commit(gitRun(t, f.repo, "rev-parse", "HEAD"))
	cand.CandidateCommit = &commit

	c := newCollector(t, f, nil)
	d, err := c.Collect(cand)
	vs := asViolations(t, err)
	if !hasCode(vs, model.CodeArtifactOutOfScope) {
		t.Fatalf("越界改动必须触发 E321，实际 %v", vs)
	}
	if d.Completion.Decision == model.DecisionDone {
		t.Fatal("越界交付不得 done")
	}
}

// TestCollectRejectsMissingCommit 覆盖 CLI 报了一个仓库里不存在的提交。
func TestCollectRejectsMissingCommit(t *testing.T) {
	f := newRepoFixture(t)
	cand := f.candidate()
	fake := model.Commit("deadbeef")
	cand.CandidateCommit = &fake

	c := newCollector(t, f, nil)
	_, err := c.Collect(cand)
	vs := asViolations(t, err)
	if !hasCode(vs, model.CodeCommitRefInvalid) {
		t.Fatalf("不存在的提交必须触发 E313，实际 %v", vs)
	}
}

// TestCollectRejectsNonAncestorBaseline 覆盖基线不是候选祖先（rebase／换分支）。
func TestCollectRejectsNonAncestorBaseline(t *testing.T) {
	f := newRepoFixture(t)
	// 另起一个分支做一次提交，作为「不属于候选链的基线」。
	gitRun(t, f.repo, "checkout", "-b", "other", string(f.baseline))
	writeFile(t, f.repo, "internal/x/c.go", "package x\n")
	gitRun(t, f.repo, "add", ".")
	gitRun(t, f.repo, "commit", "-m", "other branch")
	other := model.Commit(gitRun(t, f.repo, "rev-parse", "HEAD"))
	gitRun(t, f.repo, "checkout", "main")

	cand := f.candidate()
	cand.BaselineCommit = other

	c := newCollector(t, f, nil)
	_, err := c.Collect(cand)
	vs := asViolations(t, err)
	if !hasCode(vs, model.CodeCommitRefInvalid) {
		t.Fatalf("非祖先基线必须触发 E313，实际 %v", vs)
	}
}

// TestCollectFileOnlyInWorktree 覆盖「文件只在磁盘、不在提交里」：不可恢复，不算产物。
func TestCollectFileOnlyInWorktree(t *testing.T) {
	f := newRepoFixture(t)
	writeFile(t, f.repo, "internal/x/dirty.go", "package x\n// 未提交\n")

	cand := f.candidate()
	cand.Artifacts = append(cand.Artifacts, model.ArtifactCandidate{
		Kind: model.ArtifactFile, Ref: "internal/x/dirty.go", DeclaredBy: model.DeclaredByCLI,
	})

	c := newCollector(t, f, nil)
	d, err := c.Collect(cand)
	vs := asViolations(t, err)
	if !hasCode(vs, model.CodeArtifactMissing) {
		t.Fatalf("未提交文件必须触发 E320，实际 %v", vs)
	}
	for _, a := range d.Artifacts {
		if a.Ref == "internal/x/dirty.go" && a.SHA256 != nil {
			// 实体确实在磁盘上，哈希可以算；但绝不能因此让它支撑 done。
			if d.Completion.Decision == model.DecisionDone {
				t.Fatal("不可恢复的文件产物不得 done")
			}
		}
	}
}

// TestCollectMissingArtifact 覆盖 CLI 声明了不存在的提交产物（E320）。
func TestCollectMissingArtifact(t *testing.T) {
	f := newRepoFixture(t)
	cand := f.candidate()
	cand.Artifacts = append(cand.Artifacts, model.ArtifactCandidate{
		Kind: model.ArtifactCommit, Ref: "cafebabe", DeclaredBy: model.DeclaredByCLI,
	})

	c := newCollector(t, f, nil)
	_, err := c.Collect(cand)
	vs := asViolations(t, err)
	if !hasCode(vs, model.CodeArtifactMissing) {
		t.Fatalf("不存在的提交产物必须触发 E320，实际 %v", vs)
	}
}

// TestCollectContentHashUnverifiable 覆盖内容哈希产物：没有实体锚点就不算产物。
func TestCollectContentHashUnverifiable(t *testing.T) {
	f := newRepoFixture(t)
	cand := f.candidate()
	cand.Artifacts = append(cand.Artifacts, model.ArtifactCandidate{
		Kind:       model.ArtifactContentHash,
		Ref:        strings.Repeat("a", 64),
		DeclaredBy: model.DeclaredByCLI,
	})

	c := newCollector(t, f, nil)
	_, err := c.Collect(cand)
	vs := asViolations(t, err)
	if !hasCode(vs, model.CodeArtifactMissing) {
		t.Fatalf("内容哈希必须被判为无法核实，实际 %v", vs)
	}
}

// doneCandidate 补齐「验证 + 评审 + 合入」，用于验证 done 的正向条件。
func (f repoFixture) doneCandidate(evidence []string) Candidate {
	cand := f.candidate()
	commit := f.cand
	targetVersion := model.VersionNo(1)
	cand.Verifications = []model.Verification{{
		VerificationID:    "vfy_001",
		Command:           "go test ./...",
		TargetTaskVersion: targetVersion,
		TargetCommit:      &commit,
		Result:            model.VerifyPass,
		Evidence:          evidence,
		RanAt:             fixedNow,
	}}
	cand.Review = model.Review{Required: true, Verdict: model.VerdictApproved, Reviewer: "r1", ReviewedAt: &fixedNow}
	cand.Merge = model.Merge{Required: true, Merged: true, MergedCommit: &commit, MergedAt: &fixedNow}
	return cand
}

// TestCollectDoneWhenAllConditionsHold 覆盖 done 的正向路径。
func TestCollectDoneWhenAllConditionsHold(t *testing.T) {
	f := newRepoFixture(t)
	c := newCollector(t, f, nil, WithClock(func() time.Time { return fixedNow }))

	d, err := c.Collect(f.doneCandidate([]string{"exit_code:0", "ok  	github.com/kiwh77/maw/internal/x	0.02s"}))
	if err != nil {
		t.Fatalf("条件齐备时应无违反：%v", err)
	}
	if d.Completion.Decision != model.DecisionDone {
		t.Fatalf("应判 done，实际 %s（%s）", d.Completion.Decision, d.Completion.Reason)
	}
}

// TestCollectExitCodeOnlyCannotDone 覆盖「只有退出码 0」：不得 done（E333）。
func TestCollectExitCodeOnlyCannotDone(t *testing.T) {
	f := newRepoFixture(t)
	c := newCollector(t, f, nil)

	d, err := c.Collect(f.doneCandidate([]string{"exit_code:0"}))
	// 结论是 pending，但「只有退出码却声称 pass」仍是一条硬违反，必须报出来。
	if vs := asViolations(t, err); !hasCode(vs, model.CodeVerificationExitOnly) {
		t.Fatalf("只有退出码的通过必须触发 E333，实际 %v", vs)
	}
	if d.Completion.Decision != model.DecisionPending {
		t.Fatalf("只有退出码 0 时只能是 pending，实际 %s", d.Completion.Decision)
	}
	if !strings.Contains(d.Completion.Reason, "退出码") && !strings.Contains(d.Completion.Reason, "证据") {
		t.Fatalf("reason 应指出证据不足，实际 %q", d.Completion.Reason)
	}
}

// TestCollectStaleCommitVerification 覆盖「旧提交上的验证不能给新提交背书」（E331）。
func TestCollectStaleCommitVerification(t *testing.T) {
	f := newRepoFixture(t)
	cand := f.doneCandidate([]string{"exit_code:0", "ok"})
	old := f.baseline
	cand.Verifications[0].TargetCommit = &old

	c := newCollector(t, f, nil)
	d, err := c.Collect(cand)
	if vs := asViolations(t, err); !hasCode(vs, model.CodeVerificationStaleCommit) {
		t.Fatalf("旧提交上的验证必须触发 E331，实际 %v", vs)
	}
	if d.Completion.Decision != model.DecisionPending {
		t.Fatalf("验证针对旧提交时不得 done，实际 %s", d.Completion.Decision)
	}
}

// TestCollectRunNotCompleted 覆盖运行结果缺失／未知：交付不得通过。
func TestCollectRunNotCompleted(t *testing.T) {
	f := newRepoFixture(t)
	cand := f.doneCandidate([]string{"exit_code:0", "ok"})
	cand.RunOutcome = model.OutcomeUnknown

	c := newCollector(t, f, nil)
	d, err := c.Collect(cand)
	if err != nil {
		t.Fatalf("pending 是合法结论：%v", err)
	}
	if d.Completion.Decision != model.DecisionPending {
		t.Fatalf("运行 unknown 时不得 done，实际 %s", d.Completion.Decision)
	}
	if !strings.Contains(d.Completion.Reason, "completed") {
		t.Fatalf("reason 应点名运行未完成，实际 %q", d.Completion.Reason)
	}
}

// TestFreezeIsIdempotent 覆盖重复收取：同一份候选只产生一次交付，时间不刷新。
func TestFreezeIsIdempotent(t *testing.T) {
	f := newRepoFixture(t)
	store := NewMemoryStore()
	c := newCollector(t, f, store, WithClock(func() time.Time { return fixedNow }))

	first, created, err := c.Freeze(f.candidate())
	if err != nil || !created {
		t.Fatalf("首次冻结应创建：created=%v err=%v", created, err)
	}

	later := fixedNow.Add(time.Hour)
	c2 := newCollector(t, f, store, WithClock(func() time.Time { return later }))
	second, created, err := c2.Freeze(f.candidate())
	if err != nil {
		t.Fatalf("重复冻结不应失败：%v", err)
	}
	if created {
		t.Fatal("重复收取不得创建第二次交付")
	}
	if second.DeliveryID != first.DeliveryID {
		t.Fatalf("身份必须一致：%s vs %s", first.DeliveryID, second.DeliveryID)
	}
	if !second.DeliveredAt.Equal(first.DeliveredAt) {
		t.Fatalf("冻结时间不得刷新：%s vs %s", first.DeliveredAt, second.DeliveredAt)
	}
}

// TestFreezeSupersedeDemotesPrevious 覆盖重复交付：旧交付不得保留 done（E370）。
func TestFreezeSupersedeDemotesPrevious(t *testing.T) {
	f := newRepoFixture(t)
	store := NewMemoryStore()
	c := newCollector(t, f, store, WithClock(func() time.Time { return fixedNow }))

	first, _, err := c.Freeze(f.doneCandidate([]string{"exit_code:0", "ok"}))
	if err != nil {
		t.Fatalf("首次冻结失败：%v", err)
	}
	if first.Completion.Decision != model.DecisionDone {
		t.Fatalf("首次应 done，实际 %s", first.Completion.Decision)
	}

	// 再来一次交付：换个候选提交，并用 supersedes 指向第一次。
	writeFile(t, f.repo, "internal/x/a.go", "package x\n\n// redo\n")
	gitRun(t, f.repo, "add", ".")
	gitRun(t, f.repo, "commit", "-m", "redo")
	redo := model.Commit(gitRun(t, f.repo, "rev-parse", "HEAD"))

	cand := f.doneCandidate([]string{"exit_code:0", "ok"})
	cand.CandidateCommit = &redo
	cand.Verifications[0].TargetCommit = &redo
	cand.Merge.MergedCommit = &redo
	supersede := first.DeliveryID
	cand.Supersedes = &supersede

	second, created, err := c.Freeze(cand)
	if err != nil || !created {
		t.Fatalf("第二次冻结应创建：created=%v err=%v", created, err)
	}
	if second.Supersedes == nil || *second.Supersedes != first.DeliveryID {
		t.Fatal("新交付必须记住它取代了谁")
	}

	stored, ok, err := store.Get(first.DeliveryID)
	if err != nil || !ok {
		t.Fatalf("旧交付应还在存储里：ok=%v err=%v", ok, err)
	}
	if stored.Completion.Decision == model.DecisionDone {
		t.Fatal("被取代的交付不得保留 done")
	}
	if stored.SupersededBy == nil || *stored.SupersededBy != second.DeliveryID {
		t.Fatal("旧交付必须记录 superseded_by")
	}
}

// TestCollectSurvivesDirectoryReuse 覆盖验收第二条的前半句：
// 目录被复用（文件删掉）后，提交里的产物仍能核实。
func TestCollectSurvivesDirectoryReuse(t *testing.T) {
	f := newRepoFixture(t)
	c := newCollector(t, f, nil)

	before, err := c.Collect(f.candidate())
	if err != nil {
		t.Fatalf("首次收取失败：%v", err)
	}

	// 模拟下一道题重置目录：工作区文件被删掉，但 git 对象还在。
	if err := os.Remove(filepath.Join(f.repo, "internal/x/a.go")); err != nil {
		t.Fatalf("删文件失败：%v", err)
	}

	after, err := c.Collect(f.candidate())
	if err != nil {
		t.Fatalf("目录复用后仍应能核实：%v", err)
	}

	hashOf := func(d model.Delivery) string {
		for _, a := range d.Artifacts {
			if a.Kind == model.ArtifactFile && a.Ref == "internal/x/a.go" {
				if a.SHA256 == nil {
					return ""
				}
				return string(*a.SHA256)
			}
		}
		return ""
	}
	if hashOf(before) == "" {
		t.Fatal("首次收取应算出文件哈希")
	}
	if hashOf(after) != hashOf(before) {
		t.Fatalf("目录复用后哈希必须仍然一致：%q vs %q", hashOf(before), hashOf(after))
	}
}

// TestNewGuards 覆盖构造期拒绝：没有范围就没有越界判断。
func TestNewGuards(t *testing.T) {
	if _, err := New(testProject, t.TempDir(), nil, nil); err == nil {
		t.Fatal("空写盘范围必须被拒绝")
	}
	if _, err := New("bad", t.TempDir(), []string{"a/**"}, nil); err == nil {
		t.Fatal("非法 project_id 必须被拒绝")
	}
	if _, err := New(testProject, "", []string{"a/**"}, nil); err == nil {
		t.Fatal("空仓库路径必须被拒绝")
	}
}

// TestCollectRejectsManualDeclaredArtifact 覆盖候选里混入人工确认产物：来源不对。
func TestCollectRejectsManualDeclaredArtifact(t *testing.T) {
	f := newRepoFixture(t)
	cand := f.candidate()
	cand.Artifacts[0].DeclaredBy = model.DeclaredByManual

	c := newCollector(t, f, nil)
	_, err := c.Collect(cand)
	vs := asViolations(t, err)
	if !hasCode(vs, model.CodeDeliveryFieldInvalid) {
		t.Fatalf("人工确认产物不得作为运行候选，实际 %v", vs)
	}
}

// TestCollectRejectsBadBinding 覆盖三项绑定缺失（E310–E312）。
func TestCollectRejectsBadBinding(t *testing.T) {
	f := newRepoFixture(t)
	c := newCollector(t, f, nil)

	for _, mutate := range []func(*Candidate){
		func(cand *Candidate) { cand.TaskInstance = "" },
		func(cand *Candidate) { cand.RunID = "" },
		func(cand *Candidate) { cand.TaskVersion = 0 },
	} {
		cand := f.candidate()
		mutate(&cand)
		if _, err := c.Collect(cand); err == nil {
			t.Fatalf("缺绑定项必须被拒绝：%+v", cand)
		}
	}
}
