package nongit

import (
	"context"
	"errors"
	"os"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/workspace"
)

// 准备步骤名，失败时写进 Failure.Stage，便于定位是哪一步没成。
const (
	StageOccupancy = "occupancy"
	StageScene     = "scene"
	StagePrepare   = "prepare"
	StageVerify    = "verify"
)

// Mode 是本次准备对任务目录做的事。
type Mode string

const (
	// ModeCreate 是新建任务目录（此前不存在）。
	ModeCreate Mode = "create"
	// ModeReuse 是原地复用：目录已存在，内容一律保留，不清理、不重置。
	ModeReuse Mode = "reuse"
)

// Result 是一次成功的准备。
type Result struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	Mode   Mode   `json:"mode"`
	Layout Layout `json:"layout"`
	// Scene 是动目录之前的现场，供事后核对「原来是什么样」。
	Scene Scene `json:"scene"`
	// Preserved 是复用时保留下来的第一层条目：这些产物没有被覆盖。
	// 空目录复用（只有建到一半的残留）不会出现在这里。
	Preserved []string `json:"preserved,omitempty"`
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

// Prepare 按固定顺序准备任务目录：采集现场 → 规范化路径的独占检查 → 新建或原地复用 → 核对结果。
//
// 独占检查一定发生在任何 mkdir 之前：被占用的目录连目录都不会被建出来，
// 更不会碰到里面已有的产物。检查用的键是 plan.Path（已规范化），不是调用方给的原始写法。
//
// 复用不做任何清理：本包从不删除目录内容，「复用」的语义就是在这个目录里继续。
// 该不该清理属于验收之后的事（VN-505），在验收前删东西等于毁掉唯一一份产物。
func Prepare(ctx context.Context, idx workspace.Index, plan Plan) (Result, error) {
	before := capture(plan.Path)
	if idx == nil {
		// 没有索引就等于没有独占检查；宁可失败，也不默默裸奔。
		return Result{}, fail(StageOccupancy, plan.Path, before, before,
			invalidSpec("必须提供占用索引 workspace.Index：没有独占检查就不允许准备目录 %q", plan.Path))
	}
	occ, err := idx.WorkspaceOccupant(ctx, plan.Path)
	if err != nil {
		return Result{}, fail(StageOccupancy, plan.Path, before, before,
			model.PortFailuref(model.PortErrConflict, "读取 %q 的占用失败：%v", plan.Path, err))
	}
	if occ != nil && occ.Blocks() {
		return Result{}, fail(StageOccupancy, plan.Path, before, before, occupancyConflict(plan.Path, occ))
	}

	res := Result{Root: plan.Root, Path: plan.Path, Layout: plan.Layout, Scene: before}
	switch {
	case before.InGit():
		// 任务目录已经落在 Git 工作树里（例如产物根下嵌了一个仓库）。
		// 非 Git 实现没有基线提交可回溯，接管仓库内文档等于放弃可回溯。
		return Result{}, fail(StageScene, plan.Path, before, before, model.PortFailuref(model.PortErrConflict,
			"%q 位于 Git 工作树 %s 内：仓库内文档走 Git 工作空间（internal/workspace），非 Git 目录不接管",
			plan.Path, before.GitToplevel))
	case !before.Exists:
		if err := ensureRoot(plan.Root); err != nil {
			return Result{}, fail(StageScene, plan.Path, before, before, err)
		}
		if err := os.MkdirAll(plan.Path, 0o755); err != nil {
			return Result{}, fail(StagePrepare, plan.Path, before, capture(plan.Path),
				model.PortFailuref(model.PortErrApplication, "创建任务目录 %q 失败：%v", plan.Path, err))
		}
		res.Mode = ModeCreate
	case before.IsForeign():
		return Result{}, fail(StageScene, plan.Path, before, before, model.PortFailuref(model.PortErrConflict,
			"%q 已存在但不是目录：不覆盖、不删除，请人工确认后换路径或先给它改名", plan.Path))
	default:
		res.Mode = ModeReuse
		res.Preserved = before.Entries
	}

	if err := verifyPrepared(plan); err != nil {
		return Result{}, fail(StageVerify, plan.Path, before, capture(plan.Path), err)
	}
	return res, nil
}

// ensureRoot 核对产物根仍可用：存在、是目录、不在 Git 工作树里。
//
// 不代建产物根：产物根是调用方声明的工程目录，凭空造一个只会把产物写进一个
// 「看起来像工程」的空目录，事后没人知道它该属于谁。
func ensureRoot(root string) error {
	if !dirExists(root) {
		return model.PortFailuref(model.PortErrConflict,
			"产物根 %q 不可用（不存在或不是目录）：非 Git 产物根必须由调用方事先建好，本包不代建", root)
	}
	if top := gitToplevel(root); top != "" {
		return model.PortFailuref(model.PortErrConflict,
			"产物根 %q 位于 Git 工作树 %s 内：仓库内文档走 Git 工作空间（internal/workspace），非 Git 目录不接管", root, top)
	}
	return nil
}

// verifyPrepared 核对准备结果：目录真的在、仍是目录、仍在本产物根下且仍不属于 Git。
func verifyPrepared(plan Plan) error {
	after := capture(plan.Path)
	if !after.Exists || !after.IsDir {
		return model.PortFailuref(model.PortErrApplication, "%q 准备后不是目录", plan.Path)
	}
	if after.InGit() {
		return model.PortFailuref(model.PortErrConflict,
			"%q 准备后落在 Git 工作树 %s 内：非 Git 目录实现不接管仓库内容", plan.Path, after.GitToplevel)
	}
	if !within(plan.Root, plan.Path) {
		return model.PortFailuref(model.PortErrConflict,
			"%q 不在产物根 %q 下：拒绝在产物根之外留下目录", plan.Path, plan.Root)
	}
	return ensureRoot(plan.Root)
}

// occupancyConflict 把占用写成 E240：同一资源已有有效持有者。
func occupancyConflict(path string, occ *workspace.Occupant) error {
	return model.Violationf(model.CodeDoubleOccupancy, "workspace.path",
		"%s 仍被占用（%s）：未知执行与待冻结结果继续占着原目录，复用会覆盖已有产物", path, occ.Describe())
}

func fail(stage, path string, before, after Scene, err error) error {
	return &Failure{Stage: stage, Path: path, Before: before, After: after, Err: err}
}
