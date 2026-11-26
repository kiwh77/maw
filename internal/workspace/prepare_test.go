package workspace_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/workspace"
)

// gitRun 在指定目录跑 git，失败即终止测试。
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// initRepo 建一个带一次提交的临时仓库，返回规范化后的工程根。
func initRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init", "-b", "main")
	gitRun(t, repo, "config", "user.email", "t@t")
	gitRun(t, repo, "config", "user.name", "t")
	gitRun(t, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "init")
	root, err := workspace.CanonicalPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func headCommit(t *testing.T, dir string) string {
	t.Helper()
	return gitRun(t, dir, "rev-parse", "HEAD")
}

func curBranch(t *testing.T, dir string) string {
	t.Helper()
	return gitRun(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

func request(repo, branch string) workspace.Request {
	return workspace.Request{
		ProjectID:  "0123456789abcdef",
		Instance:   "ti_abc123",
		RunID:      "run_abc123",
		Generation: 1,
		Agent:      "codebuddy-demo-1",
		RepoRoot:   repo,
		Branch:     branch,
	}
}

// recordingIndex 记录被查询的路径，并按脚本返回占用。
type recordingIndex struct {
	queried []string
	occ     *workspace.Occupant
	err     error
}

func (r *recordingIndex) WorkspaceOccupant(_ context.Context, path string) (*workspace.Occupant, error) {
	r.queried = append(r.queried, path)
	return r.occ, r.err
}

func mustFreeze(t *testing.T, req workspace.Request) workspace.Plan {
	t.Helper()
	plan, err := workspace.Freeze(req)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	return plan
}

// TestFreezeFixesBaselineWithoutTouchingDisk 覆盖「基线冻结」：Freeze 只读，
// 基线必须是可回溯的提交，不能留一个会随 fetch 漂移的分支名。
func TestFreezeFixesBaselineWithoutTouchingDisk(t *testing.T) {
	repo := initRepo(t)
	plan := mustFreeze(t, request(repo, "agent/demo/T-1"))

	if want := filepath.Join(repo, workspace.WorktreesDir, "codebuddy-demo-1"); plan.Path != want {
		t.Fatalf("默认目录应按 Agent 复用，得到 %s，期望 %s", plan.Path, want)
	}
	if plan.Baseline != model.Commit(headCommit(t, repo)) {
		t.Fatalf("基线未冻结到 HEAD：%s vs %s", plan.Baseline, headCommit(t, repo))
	}
	if plan.Existing {
		t.Fatal("Freeze 不该创建目录")
	}
	if _, err := os.Stat(plan.Path); !os.IsNotExist(err) {
		t.Fatalf("Freeze 之后目录不应存在：%v", err)
	}
}

// TestPrepareCreatesThenReusesAgentDir 覆盖「默认复用」：同一 Agent 的下一题沿用同一目录，
// 已跟踪改动被重建到基线，被忽略的依赖目录保留。
func TestPrepareCreatesThenReusesAgentDir(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	first := mustFreeze(t, request(repo, "agent/demo/T-1"))
	res, err := workspace.Prepare(ctx, workspace.FreeIndex(), first, workspace.Options{})
	if err != nil {
		t.Fatalf("首次准备失败：%v", err)
	}
	if res.Mode != workspace.ModeCreate {
		t.Fatalf("首次应是新建，得到 %s", res.Mode)
	}
	if got := headCommit(t, first.Path); got != string(first.Baseline) {
		t.Fatalf("首次准备后 HEAD=%s，期望基线 %s", got, first.Baseline)
	}
	if got := curBranch(t, first.Path); got != first.Branch {
		t.Fatalf("分支=%s，期望 %s", got, first.Branch)
	}

	// 上一题留下的已跟踪文件 + 被忽略的依赖目录。
	if err := os.WriteFile(filepath.Join(first.Path, "feat.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, first.Path, "add", "feat.txt")
	gitRun(t, first.Path, "commit", "-m", "feat")
	if err := os.MkdirAll(filepath.Join(first.Path, "node_modules", "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "node_modules", "dep", "index.js"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := mustFreeze(t, request(repo, "agent/demo/T-2"))
	if !second.Existing || second.Path != first.Path {
		t.Fatalf("第二题应复用 %s，得到 %s（existing=%v）", first.Path, second.Path, second.Existing)
	}
	res2, err := workspace.Prepare(ctx, workspace.FreeIndex(), second, workspace.Options{})
	if err != nil {
		t.Fatalf("复用失败：%v", err)
	}
	if res2.Mode != workspace.ModeReuse {
		t.Fatalf("第二次应复用，得到 %s", res2.Mode)
	}
	if got := curBranch(t, second.Path); got != "agent/demo/T-2" {
		t.Fatalf("复用后分支=%s", got)
	}
	if got := headCommit(t, second.Path); got != string(second.Baseline) {
		t.Fatalf("复用后 HEAD=%s，期望基线 %s", got, second.Baseline)
	}
	if _, err := os.Stat(filepath.Join(second.Path, "feat.txt")); !os.IsNotExist(err) {
		t.Fatalf("已跟踪的上一题文件应被重建掉：%v", err)
	}
	if _, err := os.Stat(filepath.Join(second.Path, "node_modules", "dep", "index.js")); err != nil {
		t.Fatalf("被忽略的依赖目录应保留：%v", err)
	}
	if n := strings.Count(gitRun(t, repo, "worktree", "list"), "\n"); n != 1 {
		t.Fatalf("应当只有一个工作树登记项，得到 %d 行", n+1)
	}
}

func activeOccupant() *workspace.Occupant {
	return &workspace.Occupant{
		Instance: "ti_other1",
		Run: model.Run{
			RunID:          "run_other",
			Instance:       "ti_other1",
			Generation:     1,
			Phase:          model.PhaseRunning,
			Reconciliation: model.ReconciliationConfirmed,
		},
	}
}

// TestOccupancyCheckedBeforeAnyGitMutation 是 G01 的核心：占用检查必须早于 reset／checkout。
// 目录里有未提交改动、且被活动运行占着时，连分支都不许新建。
func TestOccupancyCheckedBeforeAnyGitMutation(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	plan := mustFreeze(t, request(repo, "agent/demo/T-1"))
	if _, err := workspace.Prepare(ctx, workspace.FreeIndex(), plan, workspace.Options{}); err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	dirty := filepath.Join(plan.Path, "README.md")
	if err := os.WriteFile(dirty, []byte("未验收的改动\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headBefore := headCommit(t, plan.Path)

	next := mustFreeze(t, request(repo, "agent/demo/T-2"))
	idx := &recordingIndex{occ: activeOccupant()}
	_, err := workspace.Prepare(ctx, idx, next, workspace.Options{AllowDiscard: true})
	if err == nil {
		t.Fatal("被活动运行占用的目录不应复用")
	}
	if !isCode(err, model.CodeDoubleOccupancy) {
		t.Fatalf("期望 E240，得到 %v", err)
	}
	f, ok := workspace.AsFailure(err)
	if !ok || f.Stage != workspace.StageOccupancy {
		t.Fatalf("失败应停在 occupancy 步骤：%+v", f)
	}
	if len(idx.queried) != 1 || idx.queried[0] != plan.Path {
		t.Fatalf("占用检查应查询规范化路径 %s，实际 %v", plan.Path, idx.queried)
	}

	// 现场必须还在：文件没被 reset，头指针没动，也没有偷偷新建分支。
	if b, _ := os.ReadFile(dirty); string(b) != "未验收的改动\n" {
		t.Fatalf("未验收改动被破坏：%q", b)
	}
	if got := headCommit(t, plan.Path); got != headBefore {
		t.Fatalf("占用检查失败却动了 HEAD：%s → %s", headBefore, got)
	}
	if got := curBranch(t, plan.Path); got != plan.Branch {
		t.Fatalf("占用检查失败却切了分支：%s", got)
	}
	if out := gitRun(t, repo, "branch", "--list", "agent/demo/T-2"); out != "" {
		t.Fatalf("占用检查失败却建了分支：%s", out)
	}
}

// TestOccupancyBlocksWhenDirMissing 说明「独占」不只管已有目录：
// 路径还是空的也不能建，否则同一目录会冒出第二个写入者。
func TestOccupancyBlocksWhenDirMissing(t *testing.T) {
	repo := initRepo(t)
	plan := mustFreeze(t, request(repo, "agent/demo/T-1"))
	_, err := workspace.Prepare(context.Background(), &recordingIndex{occ: activeOccupant()}, plan, workspace.Options{})
	if !isCode(err, model.CodeDoubleOccupancy) {
		t.Fatalf("目录不存在但被占用时也必须拒绝：%v", err)
	}
	if _, statErr := os.Stat(plan.Path); !os.IsNotExist(statErr) {
		t.Fatalf("拒绝后不应建目录：%v", statErr)
	}
}

// TestOccupancyStates 覆盖 §4.1 的四种仍然占用与可复用状态。
func TestOccupancyStates(t *testing.T) {
	cases := []struct {
		name   string
		run    model.Run
		want   workspace.HoldState
		blocks bool
	}{
		{
			name:   "运行中",
			run:    model.Run{Phase: model.PhaseRunning, Reconciliation: model.ReconciliationConfirmed},
			want:   workspace.HoldActive,
			blocks: true,
		},
		{
			name:   "核实未知",
			run:    model.Run{Phase: model.PhaseRunning, Reconciliation: model.ReconciliationUnknown},
			want:   workspace.HoldUnknown,
			blocks: true,
		},
		{
			name:   "失败但进程未确认停止",
			run:    model.Run{Phase: model.PhaseFailed, Reconciliation: model.ReconciliationConfirmed, DeliveryFrozen: true},
			want:   workspace.HoldStopUnconfirmed,
			blocks: true,
		},
		{
			name:   "运行结束但产物待验收",
			run:    model.Run{Phase: model.PhaseCompleted, Reconciliation: model.ReconciliationConfirmed},
			want:   workspace.HoldPendingAcceptance,
			blocks: true,
		},
		{
			name:   "运行结束且产物已冻结",
			run:    model.Run{Phase: model.PhaseCompleted, Reconciliation: model.ReconciliationConfirmed, DeliveryFrozen: true},
			want:   workspace.HoldFrozen,
			blocks: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			occ := workspace.Occupant{Instance: "ti_other1", Run: c.run}
			if got := occ.State(); got != c.want {
				t.Fatalf("状态=%s，期望 %s", got, c.want)
			}
			if got := occ.Blocks(); got != c.blocks {
				t.Fatalf("Blocks=%v，期望 %v", got, c.blocks)
			}
		})
	}
}

// TestFrozenOccupancyAllowsReuse 说明「已冻结」不阻塞：那正是契约允许释放目录的条件。
func TestFrozenOccupancyAllowsReuse(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	plan := mustFreeze(t, request(repo, "agent/demo/T-1"))
	frozen := &workspace.Occupant{Instance: "ti_other1", Run: model.Run{
		RunID: "run_other", Phase: model.PhaseCompleted,
		Reconciliation: model.ReconciliationConfirmed, DeliveryFrozen: true,
	}}
	res, err := workspace.Prepare(ctx, &recordingIndex{occ: frozen}, plan, workspace.Options{})
	if err != nil {
		t.Fatalf("已冻结的占用不应阻止新建：%v", err)
	}
	if res.Mode != workspace.ModeCreate {
		t.Fatalf("期望新建，得到 %s", res.Mode)
	}
}

// TestPathAliasesCollapseToOneKey 覆盖路径别名：目录的每种写法都必须收敛成同一个键，
// 否则换个写法就能绕过独占检查。
func TestPathAliasesCollapseToOneKey(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	canonical := filepath.Join(repo, workspace.WorktreesDir, "codebuddy-demo-1")
	aliases := []string{
		canonical + string(filepath.Separator),
		filepath.Join(repo, ".", workspace.WorktreesDir, "codebuddy-demo-1"),
		filepath.Join(repo, "..", filepath.Base(repo), workspace.WorktreesDir, "codebuddy-demo-1"),
		filepath.Dir(canonical) + string(filepath.Separator) + "." + string(filepath.Separator) + "codebuddy-demo-1",
	}
	for _, alias := range aliases {
		req := request(repo, "agent/demo/T-1")
		req.Path = alias
		plan, err := workspace.Freeze(req)
		if err != nil {
			t.Fatalf("Freeze(%q): %v", alias, err)
		}
		if plan.Path != canonical {
			t.Fatalf("别名 %q 未收敛到 %q，得到 %q", alias, canonical, plan.Path)
		}
	}

	// 建好目录后，用别名再问一次：占用检查必须命中同一个目录。
	plan := mustFreeze(t, request(repo, "agent/demo/T-1"))
	if _, err := workspace.Prepare(ctx, workspace.FreeIndex(), plan, workspace.Options{}); err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	req := request(repo, "agent/demo/T-2")
	req.Path = aliases[1]
	aliasPlan := mustFreeze(t, req)
	idx := &recordingIndex{occ: activeOccupant()}
	if _, err := workspace.Prepare(ctx, idx, aliasPlan, workspace.Options{}); !isCode(err, model.CodeDoubleOccupancy) {
		t.Fatalf("别名应被同一占用挡住，得到 %v", err)
	}
	if idx.queried[0] != canonical {
		t.Fatalf("占用检查收到 %q，期望规范化路径 %q", idx.queried[0], canonical)
	}
}

// TestDirtyDirectoryNeedsExplicitDiscard 覆盖脏目录：默认拒绝，显式授权才丢弃，且丢掉了什么可核对。
func TestDirtyDirectoryNeedsExplicitDiscard(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	plan := mustFreeze(t, request(repo, "agent/demo/T-1"))
	if _, err := workspace.Prepare(ctx, workspace.FreeIndex(), plan, workspace.Options{}); err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	dirty := filepath.Join(plan.Path, "README.md")
	if err := os.WriteFile(dirty, []byte("working copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headBefore := headCommit(t, plan.Path)

	next := mustFreeze(t, request(repo, "agent/demo/T-2"))
	_, err := workspace.Prepare(ctx, workspace.FreeIndex(), next, workspace.Options{})
	if err == nil {
		t.Fatal("脏目录默认不应复用")
	}
	if !isPort(err, model.PortErrConflict) {
		t.Fatalf("脏目录应报 conflict，得到 %v", err)
	}
	f, ok := workspace.AsFailure(err)
	if !ok || f.Stage != workspace.StageScene {
		t.Fatalf("失败应停在 scene 步骤：%+v", f)
	}
	if len(f.Before.TrackedDirty) == 0 || !strings.Contains(strings.Join(f.Before.TrackedDirty, "\n"), "README.md") {
		t.Fatalf("现场应记录脏文件：%v", f.Before.TrackedDirty)
	}
	if b, _ := os.ReadFile(dirty); string(b) != "working copy\n" {
		t.Fatalf("被拒绝时不得改动工作副本：%q", b)
	}
	if got := headCommit(t, plan.Path); got != headBefore {
		t.Fatalf("被拒绝时不得移动 HEAD：%s → %s", headBefore, got)
	}

	res, err := workspace.Prepare(ctx, workspace.FreeIndex(), next, workspace.Options{AllowDiscard: true})
	if err != nil {
		t.Fatalf("显式授权后应可复用：%v", err)
	}
	if len(res.Discarded) == 0 {
		t.Fatal("丢弃了什么必须可核对")
	}
	if b, _ := os.ReadFile(dirty); string(b) != "base\n" {
		t.Fatalf("授权后应重建到基线，实际 %q", b)
	}
}

// TestForeignDirectoryIsNotTouched 覆盖「不认识就不动」：非 Git 目录与他人仓库都拒绝，
// 且目录里的文件原样保留。
func TestForeignDirectoryIsNotTouched(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	plain := filepath.Join(repo, workspace.WorktreesDir, "codebuddy-demo-1")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(plain, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Prepare(ctx, workspace.FreeIndex(), mustFreeze(t, request(repo, "agent/demo/T-1")), workspace.Options{}); !isPort(err, model.PortErrConflict) {
		t.Fatalf("非 Git 目录应拒绝，得到 %v", err)
	}
	if b, _ := os.ReadFile(keep); string(b) != "keep\n" {
		t.Fatalf("非 Git 目录里的文件被动了：%q", b)
	}

	other := initRepo(t)
	req := request(repo, "agent/demo/T-2")
	req.Path = other
	if _, err := workspace.Prepare(ctx, workspace.FreeIndex(), mustFreeze(t, req), workspace.Options{AllowDiscard: true}); !isPort(err, model.PortErrConflict) {
		t.Fatalf("他人仓库应拒绝，得到 %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, "README.md")); err != nil {
		t.Fatalf("他人仓库文件被动了：%v", err)
	}
}

// TestRepoSubdirectoryIsNotAWorktree 是最容易踩的一条：<repo>/.worktrees/<agent>
// 若是工程里的普通子目录，rev-parse --show-toplevel 会解析到工程根。把它当工作树
// reset --hard，等于把主干工作区一起重置。判据必须是「解析结果就是自己」。
func TestRepoSubdirectoryIsNotAWorktree(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	sub := filepath.Join(repo, workspace.WorktreesDir, "codebuddy-demo-1")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 主干工作区也留一处未提交改动：任何 reset 都会把它抹掉。
	mainDirty := filepath.Join(repo, "README.md")
	if err := os.WriteFile(mainDirty, []byte("主干未提交\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headBefore := headCommit(t, repo)

	if _, err := workspace.Prepare(ctx, workspace.FreeIndex(), mustFreeze(t, request(repo, "agent/demo/T-1")), workspace.Options{AllowDiscard: true}); !isPort(err, model.PortErrConflict) {
		t.Fatalf("工程子目录不得当成工作树复用，得到 %v", err)
	}
	if b, _ := os.ReadFile(mainDirty); string(b) != "主干未提交\n" {
		t.Fatalf("主干工作区被重置：%q", b)
	}
	if got := headCommit(t, repo); got != headBefore {
		t.Fatalf("主干 HEAD 被动过：%s → %s", headBefore, got)
	}
	if got := curBranch(t, repo); got != "main" {
		t.Fatalf("主干分支被动过：%s", got)
	}
}

// TestOccupancyLookupErrorKeepsScene 覆盖索引本身出错：不猜「大概没人占」，保持现场。
func TestOccupancyLookupErrorKeepsScene(t *testing.T) {
	repo := initRepo(t)
	plan := mustFreeze(t, request(repo, "agent/demo/T-1"))
	idx := &recordingIndex{err: errShim}
	_, err := workspace.Prepare(context.Background(), idx, plan, workspace.Options{})
	if !isPort(err, model.PortErrConflict) {
		t.Fatalf("索引出错应报 conflict，得到 %v", err)
	}
	if f, ok := workspace.AsFailure(err); !ok || f.Stage != workspace.StageOccupancy {
		t.Fatalf("失败应停在 occupancy 步骤：%+v", f)
	}
	if _, statErr := os.Stat(plan.Path); !os.IsNotExist(statErr) {
		t.Fatalf("索引出错时不应建目录：%v", statErr)
	}
}

// TestEmptyLeftoverDirIsRecreated 覆盖建到一半的空目录：只 rmdir，不递归删除。
func TestEmptyLeftoverDirIsRecreated(t *testing.T) {
	repo := initRepo(t)
	plan := mustFreeze(t, request(repo, "agent/demo/T-1"))
	if err := os.MkdirAll(plan.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := workspace.Prepare(context.Background(), workspace.FreeIndex(), plan, workspace.Options{})
	if err != nil {
		t.Fatalf("空残留目录应可重建：%v", err)
	}
	if !res.RemovedEmptyDir || res.Mode != workspace.ModeCreate {
		t.Fatalf("应清掉空目录后新建：%+v", res)
	}
	if got := headCommit(t, plan.Path); got != string(plan.Baseline) {
		t.Fatalf("HEAD=%s，期望基线 %s", got, plan.Baseline)
	}
}

// TestFreezeRejectsUnsafeInput 覆盖会拼进 git 参数的输入：分支名、相对路径、工程根。
func TestFreezeRejectsUnsafeInput(t *testing.T) {
	repo := initRepo(t)
	cases := map[string]func(*workspace.Request){
		"分支以短横开头":  func(r *workspace.Request) { r.Branch = "-D" },
		"分支带空格":    func(r *workspace.Request) { r.Branch = "agent demo T-1" },
		"分支为空":     func(r *workspace.Request) { r.Branch = " " },
		"相对目录":     func(r *workspace.Request) { r.Path = "worktrees/x" },
		"目录指向工程根":  func(r *workspace.Request) { r.Path = repo },
		"非 Git 工程": func(r *workspace.Request) { r.RepoRoot = t.TempDir() },
		"基线不存在":    func(r *workspace.Request) { r.Base = "no-such-ref" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := request(repo, "agent/demo/T-1")
			mutate(&req)
			if _, err := workspace.Freeze(req); err == nil {
				t.Fatal("应拒绝")
			} else if !isPort(err, model.PortErrInvalidSpec) {
				t.Fatalf("应报 invalid_spec，得到 %v", err)
			}
		})
	}
}

// TestPrepareRequiresIndex 说明「没有独占检查就不许动目录」。
func TestPrepareRequiresIndex(t *testing.T) {
	repo := initRepo(t)
	plan := mustFreeze(t, request(repo, "agent/demo/T-1"))
	if _, err := workspace.Prepare(context.Background(), nil, plan, workspace.Options{}); err == nil {
		t.Fatal("nil 索引必须拒绝")
	}
	if _, statErr := os.Stat(plan.Path); !os.IsNotExist(statErr) {
		t.Fatalf("拒绝后不应建目录：%v", statErr)
	}
}

func isCode(err error, code model.Code) bool {
	e, ok := model.AsError(err)
	return ok && e.Code == code
}

func isPort(err error, code model.PortErrorCode) bool {
	e, ok := model.AsError(err)
	return ok && e.Port == code
}

var errShim = errors.New("占用读取失败")

// TestOccupancyKeyMatchesResourceKey 锁住本包与存储层的约定：Index 收到的规范化路径，
// 经 db.Resource{Kind: LeaseWorkspace}.Key() 得到的正是那条唯一占用索引的键。
// 两边任一改动都会让独占检查查错行，所以用测试钉住而不是靠注释。
func TestOccupancyKeyMatchesResourceKey(t *testing.T) {
	repo := initRepo(t)
	plan := mustFreeze(t, request(repo, "agent/demo/T-1"))
	key, err := db.Resource{Kind: model.LeaseWorkspace, Path: plan.Path}.Key()
	if err != nil {
		t.Fatalf("资源键生成失败：%v", err)
	}
	if want := "ws:" + plan.Path; key != want {
		t.Fatalf("资源键=%q，期望 %q", key, want)
	}
	alias := filepath.Join(repo, ".", workspace.WorktreesDir, "codebuddy-demo-1") + string(filepath.Separator)
	req := request(repo, "agent/demo/T-1")
	req.Path = alias
	aliasPlan := mustFreeze(t, req)
	aliasKey, err := db.Resource{Kind: model.LeaseWorkspace, Path: aliasPlan.Path}.Key()
	if err != nil {
		t.Fatal(err)
	}
	if aliasKey != key {
		t.Fatalf("别名应得到同一个资源键：%q vs %q", aliasKey, key)
	}
}
