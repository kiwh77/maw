package nongit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/workspace"
	"github.com/kiwh77/maw/internal/workspace/nongit"
)

// fakeIndex 记录被查询的路径，并按脚本返回占用。
type fakeIndex struct {
	queried []string
	occ     *workspace.Occupant
	err     error
}

func (f *fakeIndex) WorkspaceOccupant(_ context.Context, path string) (*workspace.Occupant, error) {
	f.queried = append(f.queried, path)
	return f.occ, f.err
}

func occupant(run model.Run) *workspace.Occupant {
	return &workspace.Occupant{Instance: "ti_abc123", Run: run}
}

func freeze(t *testing.T, root string) nongit.Plan {
	t.Helper()
	plan, err := nongit.Freeze(baseRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 首次准备建目录；再次准备原地复用，已有产物必须逐字节保留。
func TestPrepareCreatesThenReusesWithoutTouchingArtifacts(t *testing.T) {
	root := nonGitRoot(t)
	plan := freeze(t, root)
	idx := &fakeIndex{}

	res, err := nongit.Prepare(context.Background(), idx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != nongit.ModeCreate {
		t.Fatalf("首次准备应是新建，得到 %q", res.Mode)
	}
	if len(idx.queried) != 1 || idx.queried[0] != plan.Path {
		t.Fatalf("占用检查应针对规范化路径 %q，实际查询 %v", plan.Path, idx.queried)
	}

	draft := filepath.Join(plan.Path, "draft.md")
	if err := os.MkdirAll(filepath.Join(plan.Path, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draft, []byte("第一稿\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plan.Path, "assets", "a.txt"), []byte("asset\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 重复准备：同一计划再来一次，必须识别为复用且不动任何内容。
	again, err := nongit.Prepare(context.Background(), &fakeIndex{}, freeze(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if again.Mode != nongit.ModeReuse {
		t.Fatalf("第二次准备应是复用，得到 %q", again.Mode)
	}
	if got, want := strings.Join(again.Preserved, ","), "assets,draft.md"; got != want {
		t.Fatalf("保留条目应为 %q，得到 %q", want, got)
	}
	if got := readFile(t, draft); got != "第一稿\n" {
		t.Fatalf("复用把已有产物改了：%q", got)
	}
	if got := readFile(t, filepath.Join(plan.Path, "assets", "a.txt")); got != "asset\n" {
		t.Fatalf("复用把已有产物改了：%q", got)
	}
}

// 占用状态矩阵：未知执行与待冻结结果继续占用原目录；只有产物已冻结才允许复用。
func TestPrepareOccupancyMatrix(t *testing.T) {
	cases := []struct {
		name    string
		occ     *workspace.Occupant
		blocked bool
	}{
		{"运行仍进行", occupant(model.Run{Phase: model.PhaseRunning}), true},
		{"核实状态未知", occupant(model.Run{Phase: model.PhaseCompleted, Reconciliation: model.ReconciliationUnknown}), true},
		{"进程停止未确认", occupant(model.Run{Phase: model.PhaseCancelled}), true},
		{"产物待验收", occupant(model.Run{Phase: model.PhaseCompleted}), true},
		{"产物已冻结", occupant(model.Run{Phase: model.PhaseCompleted, DeliveryFrozen: true}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := nonGitRoot(t)
			plan := freeze(t, root)
			res, err := nongit.Prepare(context.Background(), &fakeIndex{occ: c.occ}, plan)
			if !c.blocked {
				if err != nil {
					t.Fatalf("产物已冻结时应允许准备，得到 %v", err)
				}
				if res.Mode != nongit.ModeCreate {
					t.Fatalf("首次准备应新建，得到 %q", res.Mode)
				}
				return
			}
			if err == nil {
				t.Fatal("必须拒绝准备")
			}
			if !errors.Is(err, model.Violation(model.CodeDoubleOccupancy, "", "")) {
				t.Fatalf("应返回 E240，得到 %v", err)
			}
			if _, statErr := os.Stat(plan.Path); !os.IsNotExist(statErr) {
				t.Fatalf("被占用时不得建目录：%v", statErr)
			}
		})
	}
}

// 失败要带现场：谁挡住的、目录里原来有什么，都要看得到，且现场不能被改。
func TestPrepareBlockedCarriesScene(t *testing.T) {
	root := nonGitRoot(t)
	plan := freeze(t, root)
	if err := os.MkdirAll(plan.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(plan.Path, "keep.md")
	if err := os.WriteFile(keep, []byte("待验收\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := nongit.Prepare(context.Background(),
		&fakeIndex{occ: occupant(model.Run{Phase: model.PhaseRunning})}, plan)
	if err == nil {
		t.Fatal("运行进行中时必须拒绝")
	}
	f, ok := nongit.AsFailure(err)
	if !ok {
		t.Fatalf("应返回现场化错误，得到 %T", err)
	}
	if f.Stage != nongit.StageOccupancy {
		t.Fatalf("失败阶段应为 %q，得到 %q", nongit.StageOccupancy, f.Stage)
	}
	if got, want := strings.Join(f.Before.Entries, ","), "keep.md"; got != want {
		t.Fatalf("失败现场应列出 %q，得到 %q", want, got)
	}
	if got := readFile(t, keep); got != "待验收\n" {
		t.Fatalf("失败后现场被改动：%q", got)
	}
}

// 名字被文件占了：不覆盖、不删除，直接报冲突。
func TestPrepareRefusesFileAtPath(t *testing.T) {
	root := nonGitRoot(t)
	plan := freeze(t, root)
	if err := os.MkdirAll(filepath.Dir(plan.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan.Path, []byte("占位\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := nongit.Prepare(context.Background(), &fakeIndex{}, plan)
	if err == nil {
		t.Fatal("路径已是文件时必须拒绝")
	}
	if !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("错误应说明不是目录，得到 %v", err)
	}
	if got := readFile(t, plan.Path); got != "占位\n" {
		t.Fatalf("占位文件被覆盖：%q", got)
	}
}

// 产物根不是 Git，但任务目录里嵌了一个仓库：这种目录归 Git 工作空间管。
func TestPrepareRefusesGitWorktreeAtPath(t *testing.T) {
	root := nonGitRoot(t)
	plan := freeze(t, root)
	if err := os.MkdirAll(plan.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, plan.Path, "init", "-b", "main")

	_, err := nongit.Prepare(context.Background(), &fakeIndex{}, plan)
	if err == nil {
		t.Fatal("目录在 Git 工作树里时必须拒绝")
	}
	if !strings.Contains(err.Error(), "Git 工作空间") {
		t.Fatalf("错误应指回 Git 工作空间，得到 %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(plan.Path, ".git")); statErr != nil {
		t.Fatalf("拒绝时不应动仓库：%v", statErr)
	}
}

// 产物根在准备前消失：失败，不代建。
func TestPrepareRefusesVanishedRoot(t *testing.T) {
	root := nonGitRoot(t)
	plan := freeze(t, root)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	_, err := nongit.Prepare(context.Background(), &fakeIndex{}, plan)
	if err == nil {
		t.Fatal("产物根不存在时必须拒绝")
	}
	if !strings.Contains(err.Error(), "不代建") {
		t.Fatalf("错误应说明不代建产物根，得到 %v", err)
	}
}

// 没有占用索引就不许准备：否则独占检查形同虚设。
func TestPrepareRequiresIndex(t *testing.T) {
	root := nonGitRoot(t)
	plan := freeze(t, root)

	_, err := nongit.Prepare(context.Background(), nil, plan)
	if err == nil {
		t.Fatal("index 为 nil 时必须拒绝")
	}
	if !errors.Is(err, model.PortFailure(model.PortErrInvalidSpec, "")) {
		t.Fatalf("应归类为 invalid_spec，得到 %v", err)
	}
	if _, statErr := os.Stat(plan.Path); !os.IsNotExist(statErr) {
		t.Fatalf("拒绝时不得建目录：%v", statErr)
	}
}

// 两个产物根用同一个 Agent 名：目录互不相同，准备其中一个不碰另一个。
func TestPrepareKeepsOtherRootUntouched(t *testing.T) {
	rootA := nonGitRoot(t)
	rootB := nonGitRoot(t)
	planA := freeze(t, rootA)
	planB := freeze(t, rootB)
	if planA.Path == planB.Path {
		t.Fatalf("不同产物根不应算出同一路径 %q", planA.Path)
	}

	if _, err := nongit.Prepare(context.Background(), &fakeIndex{}, planA); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(planA.Path, "result.md")
	if err := os.WriteFile(artifact, []byte("A 的产物\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := nongit.Prepare(context.Background(), &fakeIndex{}, planB); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, artifact); got != "A 的产物\n" {
		t.Fatalf("准备 B 时动了 A 的产物：%q", got)
	}
	if _, err := os.Stat(filepath.Join(planB.Path, "result.md")); !os.IsNotExist(err) {
		t.Fatalf("B 的任务目录不该出现 A 的产物：%v", err)
	}
}

// 占用索引报错时按冲突返回，且不动目录。
func TestPrepareIndexErrorIsConflict(t *testing.T) {
	root := nonGitRoot(t)
	plan := freeze(t, root)

	_, err := nongit.Prepare(context.Background(), &fakeIndex{err: errors.New("db 掉了")}, plan)
	if err == nil {
		t.Fatal("读占用失败时必须拒绝")
	}
	if !errors.Is(err, model.PortFailure(model.PortErrConflict, "")) {
		t.Fatalf("应归类为 conflict，得到 %v", err)
	}
	if _, statErr := os.Stat(plan.Path); !os.IsNotExist(statErr) {
		t.Fatalf("读占用失败时不得建目录：%v", statErr)
	}
}
