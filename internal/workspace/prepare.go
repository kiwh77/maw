package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
)

// 准备步骤名，失败时写进 Failure.Stage，便于定位是哪一步没成。
const (
	StageOccupancy = "occupancy"
	StageScene     = "scene"
	StagePrepare   = "prepare"
	StageVerify    = "verify"
)

// Options 是准备时的显式授权项；默认一律不放行会丢数据的操作。
type Options struct {
	// AllowDiscard 显式接受丢掉该目录里已跟踪文件的未提交改动。
	// 默认 false：脏目录直接拒绝复用，因为 reset --hard 之后那些改动找不回来。
	AllowDiscard bool
}

// Result 是一次成功的准备。
type Result struct {
	Path     string       `json:"path"`
	Branch   string       `json:"branch"`
	Baseline model.Commit `json:"baseline_commit"`
	Mode     Mode         `json:"mode"`
	// Scene 是动目录之前的现场，供事后核对「原来是什么样」。
	Scene Scene `json:"scene"`
	// Discarded 是被 reset 丢掉的已跟踪改动（仅在 AllowDiscard 时非空）。
	Discarded []string `json:"discarded,omitempty"`
	// RemovedEmptyDir 表示清掉了一个空的残留目录（只 rmdir，不递归删除）。
	RemovedEmptyDir bool `json:"removed_empty_dir,omitempty"`
}

// Failure 是一次准备失败的现场。现场只读采集，失败后目录保持原样。
type Failure struct {
	Stage string `json:"stage"`
	Path  string `json:"path"`
	// Before 是动目录之前采集的现场；After 是失败时刻重新采集的现场。
	Before Scene `json:"before"`
	After  Scene `json:"after"`
	Err    error `json:"-"`
}

// Error 输出「哪一步、哪个目录、为什么」。
func (f *Failure) Error() string {
	if f == nil {
		return ""
	}
	msg := f.Stage
	if f.Path != "" {
		msg += " " + f.Path
	}
	if f.Err != nil {
		msg += ": " + f.Err.Error()
	}
	return msg
}

// Unwrap 保留底层错误，便于 errors.Is 判定 E240／端口错误码。
func (f *Failure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Err
}

// AsFailure 取回现场化错误。
func AsFailure(err error) (*Failure, bool) {
	var f *Failure
	if errors.As(err, &f) {
		return f, true
	}
	return nil, false
}

// Prepare 按固定顺序准备目录：采集现场 → 规范化路径的独占检查 → 复用或新建 → 校验头指针。
//
// 独占检查一定发生在任何 reset／checkout 之前：被占用的目录连 fetch 都不会触发，
// 更不会动它的分支和文件。检查用的键是 plan.Path（已规范化），不是调用方给的原始写法。
func Prepare(ctx context.Context, idx Index, plan Plan, opts Options) (Result, error) {
	before := capture(plan.Path)
	if idx == nil {
		// 没有索引就等于没有独占检查；宁可失败，也不默默裸奔。
		return Result{}, fail(StageOccupancy, plan.Path, before, before,
			invalidSpec("必须提供占用索引 Index：没有独占检查就不允许准备目录 %q", plan.Path))
	}
	occ, err := idx.WorkspaceOccupant(ctx, plan.Path)
	if err != nil {
		return Result{}, fail(StageOccupancy, plan.Path, before, before,
			model.PortFailuref(model.PortErrConflict, "读取 %q 的占用失败：%v", plan.Path, err))
	}
	if occ != nil && occ.Blocks() {
		return Result{}, fail(StageOccupancy, plan.Path, before, before, occupancyConflict(plan.Path, occ))
	}

	res := Result{Path: plan.Path, Branch: plan.Branch, Baseline: plan.Baseline, Scene: before, Mode: ModeReuse}
	switch {
	case !before.Exists:
		res.Mode = ModeCreate
		if err := gitops.CreateWorktree(plan.Repo, plan.Path, plan.Branch, string(plan.Baseline)); err != nil {
			return Result{}, fail(StagePrepare, plan.Path, before, capture(plan.Path), err)
		}
	case before.IsForeign():
		if !before.Empty {
			return Result{}, fail(StageScene, plan.Path, before, before, model.PortFailuref(model.PortErrConflict,
				"%q 已存在但%s（%d 个条目），不复用也不覆盖；请人工确认后再处理",
				plan.Path, foreignReason(before, plan.Path), before.Entries))
		}
		// 空目录只是建到一半的残留：只 rmdir，绝不递归删除。
		if err := os.Remove(plan.Path); err != nil {
			return Result{}, fail(StageScene, plan.Path, before, capture(plan.Path),
				model.PortFailuref(model.PortErrConflict, "清理空目录 %q 失败：%v", plan.Path, err))
		}
		res.Mode = ModeCreate
		res.RemovedEmptyDir = true
		if err := gitops.CreateWorktree(plan.Repo, plan.Path, plan.Branch, string(plan.Baseline)); err != nil {
			return Result{}, fail(StagePrepare, plan.Path, before, capture(plan.Path), err)
		}
	default:
		if !isOwnWorktree(before, plan.Repo) {
			return Result{}, fail(StageScene, plan.Path, before, before, model.PortFailuref(model.PortErrConflict,
				"%q 属于另一个仓库（toplevel=%s），不复用", plan.Path, before.Toplevel))
		}
		if len(before.TrackedDirty) > 0 && !opts.AllowDiscard {
			return Result{}, fail(StageScene, plan.Path, before, before, model.PortFailuref(model.PortErrConflict,
				"%q 有 %d 处未提交的已跟踪改动（%s），复用会 reset 掉它们；需要显式 AllowDiscard 才继续",
				plan.Path, len(before.TrackedDirty), snippet(before.TrackedDirty, 4)))
		}
		if err := gitops.EnsureAgentWorktree(plan.Repo, plan.Path, plan.Branch, string(plan.Baseline)); err != nil {
			return Result{}, fail(StagePrepare, plan.Path, before, capture(plan.Path), err)
		}
		if opts.AllowDiscard {
			res.Discarded = before.TrackedDirty
		}
	}

	if err := verifyPrepared(plan); err != nil {
		return Result{}, fail(StageVerify, plan.Path, before, capture(plan.Path), err)
	}
	return res, nil
}

// verifyPrepared 核对「基线冻结 + 分支重建」真的生效：头指针必须就是冻结的提交。
func verifyPrepared(plan Plan) error {
	head, err := home.Git(plan.Path, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return model.PortFailuref(model.PortErrApplication, "读取 %q 的 HEAD 失败：%v", plan.Path, err)
	}
	if model.Commit(head) != plan.Baseline {
		return model.PortFailuref(model.PortErrConflict,
			"%q 的 HEAD 是 %s，不是冻结基线 %s：目录没切到计划中的基线",
			plan.Path, head, plan.Baseline)
	}
	branch, err := home.Git(plan.Path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != plan.Branch {
		return model.PortFailuref(model.PortErrConflict,
			"%q 当前不在任务分支 %q 上（实际 %q）", plan.Path, plan.Branch, branch)
	}
	return nil
}

// foreignReason 说清「为什么这个目录不能当工作树用」：
// 不是 Git、是某个工作树的子目录（最常见），或本身就是别的仓库。
func foreignReason(scene Scene, path string) string {
	switch {
	case scene.Toplevel == "":
		return "不是 Git 工作区"
	case scene.Toplevel != path:
		return "是工作树 " + scene.Toplevel + " 的子目录，不是独立工作树"
	default:
		return "不是独立工作树"
	}
}

func fail(stage, path string, before, after Scene, err error) error {
	return &Failure{Stage: stage, Path: path, Before: before, After: after, Err: err}
}

func snippet(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, "; ")
	}
	return strings.Join(items[:max], "; ") + fmt.Sprintf(" …共 %d 处", len(items))
}
