package app

import (
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
)

// 退出码约定（契约 maw.collaboration.primitives/v1 §2 的事件可见性衍生）：
//
//	0  条件已满足（deps 全 done / 目标任务已 done）
//	2  超时（条件未满足，调用方据此决定重试或换路）
//	1  参数或环境错误（任务不存在、既没给 --task 也没给 --deps-of …）
//
// 2 与 1 分开的原因：超时是**正常业务结局**，不是故障；脚本要能区分
// 「还没好」和「跑错了」。这与 maw 其它命令一律 1 的约定刻意不同，
// 所以只在 wait 上生效，不改 printRes 的全局行为。

// WaitTimeoutExitCode 是超时退出码。
const WaitTimeoutExitCode = 2

// WaitInput 是 `maw wait` 的入参。
type WaitInput struct {
	// TaskID 等到该任务进入终态 done。
	TaskID string
	// DepsOf 等到该任务的**全部** deps 进入 done。
	DepsOf string
	// Timeout 超时；<=0 用默认 300s。
	Timeout time.Duration
	// Poll 轮询间隔；<=0 用默认 2s。
	Poll time.Duration
}

// WaitTick 轮询时的一帧观察结果（测试注入时钟时用）。
type WaitTick struct {
	// Satisfied 条件已满足。
	Satisfied bool
	// Unsatisfiable 条件永远不可能满足（例如依赖已取消）。
	Unsatisfiable bool
	// Reason 是一句人读理由，进结果与事件之外的输出。
	Reason string
	// Pending 是仍在等待的 dep id 列表（稳定排序）。
	Pending []string
}

type waitObservation struct {
	tick       WaitTick
	projectID  string
	elapsedMS  int64
	pollCount  int
	waitedFor  string
	terminalID string
}

// Wait 阻塞到条件满足或超时，返回结果；超时返回退出码 2 的 errx。
func Wait(cwd string, in WaitInput) (Result, error) {
	taskID := strings.TrimSpace(in.TaskID)
	depsOf := strings.TrimSpace(in.DepsOf)
	if (taskID == "") == (depsOf == "") {
		return nil, &errx.Error{
			Msg:  i18n.T("err.wait_target"),
			Next: i18n.T("next.wait_target"),
			Code: 1,
		}
	}

	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	if err := db.Init(gdb); err != nil {
		return nil, err
	}

	timeout := in.Timeout
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	poll := in.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if poll > timeout {
		poll = timeout
	}

	start := time.Now()
	deadline := start.Add(timeout)
	polls := 0
	for {
		polls++
		obs, err := observeWait(gdb, pc.ID(), taskID, depsOf)
		if err != nil {
			return nil, err
		}
		if obs.tick.Satisfied {
			return Result{
				"ok": true, "project_id": pc.ID(),
				"waited_for": obs.waitedFor, "task_id": obs.terminalID,
				"pending": []string{}, "elapsed_ms": time.Since(start).Milliseconds(),
				"polls": polls, "reason": obs.tick.Reason,
				"next": i18n.T("next.wait_done"),
			}, nil
		}
		if obs.tick.Unsatisfiable {
			return nil, &errx.Error{
				Msg:  i18n.T("err.wait_unsatisfiable") + ": " + obs.tick.Reason,
				Next: i18n.T("next.wait_unsatisfiable"),
				Code: 1,
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, &errx.Error{
				Msg: i18n.T("err.wait_timeout") + ": " + obs.tick.Reason,
				Next: i18n.T("next.wait_timeout") + " (" +
					strings.Join(obs.tick.Pending, ",") + ")",
				Code: WaitTimeoutExitCode,
			}
		}
		if remaining < poll {
			time.Sleep(remaining)
		} else {
			time.Sleep(poll)
		}
	}
}

// observeWait 读一帧事实：任务状态或依赖满足情况。只读，不改任何状态。
func observeWait(gdb, projectID, taskID, depsOf string) (waitObservation, error) {
	out := waitObservation{waitedFor: taskID}
	target := taskID
	if depsOf != "" {
		out.waitedFor = "deps:" + depsOf
		target = depsOf
	}
	out.terminalID = target

	err := withGlobalRead(gdb, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, projectID, target)
		if err != nil {
			return err
		}
		if t == nil {
			return &errx.Error{
				Msg:  i18n.T("err.unknown_task") + ": " + target,
				Next: i18n.T("next.task_list"),
				Code: 1,
			}
		}
		if depsOf == "" {
			out.tick = taskTerminalTick(t)
			return nil
		}
		all, err := db.ListTasks(tx, projectID)
		if err != nil {
			return err
		}
		out.tick = depsTick(t, all)
		return nil
	})
	return out, err
}

// taskTerminalTick 判断目标任务是否已经收工。
func taskTerminalTick(t *db.Task) WaitTick {
	switch t.Status {
	case "done":
		return WaitTick{Satisfied: true, Reason: "status=done"}
	case "cancelled":
		return WaitTick{Unsatisfiable: true, Reason: "status=cancelled"}
	}
	return WaitTick{Reason: "status=" + t.Status, Pending: []string{t.ID}}
}

// depsTick 判断 deps 是否全部 done；取消的依赖永远不可能满足，直接判死。
func depsTick(t *db.Task, all []*db.Task) WaitTick {
	byID := map[string]*db.Task{}
	for _, o := range all {
		if o != nil {
			byID[o.ID] = o
		}
	}
	var pending, dead []string
	for _, dep := range t.Deps {
		up := byID[dep]
		if up == nil {
			dead = append(dead, dep+"(missing)")
			continue
		}
		switch up.Status {
		case "done":
			// 满足
		case "cancelled":
			dead = append(dead, dep+"(cancelled)")
		default:
			pending = append(pending, dep)
		}
	}
	sort.Strings(pending)
	sort.Strings(dead)
	if len(dead) > 0 {
		return WaitTick{Unsatisfiable: true, Reason: "前置不可满足: " + strings.Join(dead, ","), Pending: pending}
	}
	if len(pending) == 0 {
		return WaitTick{Satisfied: true, Reason: "deps 全部 done"}
	}
	return WaitTick{Reason: "等待前置: " + strings.Join(pending, ","), Pending: pending}
}
