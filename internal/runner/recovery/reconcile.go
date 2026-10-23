package recovery

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/process"
)

// IdentityFile 是进程身份记录在 Run 目录里的固定文件名。
const IdentityFile = "process.json"

// State 是重启后能确认的四种现场。
type State string

const (
	// StateRunning 表示进程确实还在运行：监督关系丢了，进程没丢。
	StateRunning State = "running"
	// StateFinished 表示进程确定结束，且进程组已确认清空。
	StateFinished State = "finished"
	// StateNotStarted 表示从未启动过：没有身份记录，日志也没有记录。
	StateNotStarted State = "not_started"
	// StateUnknown 表示证据不足，无法确认。
	StateUnknown State = "unknown"
)

// Valid 报告取值是否合法。
func (s State) Valid() bool {
	switch s {
	case StateRunning, StateFinished, StateNotStarted, StateUnknown:
		return true
	}
	return false
}

// Evidence 是一次现场核实的结论与依据。
type Evidence struct {
	State State `json:"state"`
	// Identity 是读到的进程身份（没有记录时为 nil）。
	Identity *process.Identity `json:"identity,omitempty"`
	// Stop 是 process.Recover 给出的核对证据（没有身份时为 nil）。
	Stop *process.StopEvidence `json:"stop,omitempty"`
	// Journal 是本机日志的可观察事实。
	Journal journal.Status `json:"journal"`
	// Cursor 是日志游标：补交从这里往后继续。
	Cursor journal.Cursor `json:"cursor"`
	// Reason 是人话原因，直接进看板。
	Reason string `json:"reason"`
}

// KeepsResources 报告是否必须保留资源占用。
//
// 只有「确定结束」和「从未启动」才能放资源；running 与 unknown 都要留住——
// 未知归属的进程组可能还在写工作目录。
func (e Evidence) KeepsResources() bool {
	return e.State == StateRunning || e.State == StateUnknown
}

// BlocksRerun 报告是否禁止换目录重跑同一道题。
//
// 现场没确认清楚之前重跑，等于把两个进程写进同一个任务实例。
func (e Evidence) BlocksRerun() bool { return e.KeepsResources() }

// Reusable 报告工作目录能否被下一道题复用。
func (e Evidence) Reusable() bool {
	return !e.KeepsResources() && !e.Journal.Incomplete
}

// PendingReplay 报告日志里是否还有待补交的记录。
func (e Evidence) PendingReplay() bool { return e.Journal.PendingRecords > 0 }

// GuardRerun 在禁止重跑时返回可上报的错误，允许时返回 nil。
//
// 调用方（工作空间复用、下一题领取）应当无条件调用它，
// 而不是自己判断 State——判断口径只有这一处。
func (e Evidence) GuardRerun() error {
	if !e.BlocksRerun() {
		return nil
	}
	return model.Violationf(model.CodeDoubleOccupancy, "recovery.state",
		"现场未确认（%s）：%s；不得换目录重跑同一道题", e.State, e.Reason)
}

// Options 是核实的输入路径。
type Options struct {
	// Dir 是 Run 目录：身份记录与日志都在它下面。
	Dir string
	// IdentityPath 覆盖身份记录路径；空则用 Dir/IdentityFile。
	IdentityPath string
	// JournalDir 覆盖日志目录；空则用 Dir。
	JournalDir string
	// ExpectedNonce 是该 Run 冻结规格里的 nonce；给了就额外校验身份没有被挪用。
	ExpectedNonce string
	// RunID 是日志所属的运行身份。
	RunID string
	// Probe 覆盖进程核对；空则用 process.Recover。
	//
	// 抽出来是为了让「四种现场怎么判」可以脱离子进程测——
	// 真实进程的核对（PID 复用、进程组清空）由 process 包自己的测试覆盖。
	Probe Prober
}

// Prober 核对一次受管进程的身份与后代。
type Prober interface {
	Recover(id process.Identity) (process.StopEvidence, error)
}

type processProber struct{}

func (processProber) Recover(id process.Identity) (process.StopEvidence, error) {
	return process.Recover(id)
}

func (o Options) identityPath() string {
	if strings.TrimSpace(o.IdentityPath) != "" {
		return o.IdentityPath
	}
	return strings.TrimRight(o.Dir, "/") + "/" + IdentityFile
}

func (o Options) journalDir() string {
	if strings.TrimSpace(o.JournalDir) != "" {
		return o.JournalDir
	}
	return o.Dir
}

// Reconcile 核实一次 Run 的现场。
//
// 前提：监督进程已经不在（本函数只读，不和活着的监督者抢日志）。
// 返回的 Evidence 总是可读的：连日志都打不开时也会给出 unknown 与原因。
func Reconcile(opts Options) (Evidence, error) {
	if strings.TrimSpace(opts.Dir) == "" && strings.TrimSpace(opts.IdentityPath) == "" {
		return Evidence{}, model.Violation(model.CodeRuntimeFieldInvalid, "recovery.dir", "必须给出 Run 目录")
	}

	ev := Evidence{State: StateUnknown}
	ev.Journal, ev.Cursor = readJournal(opts)

	id, idErr := process.ReadIdentity(opts.identityPath())
	switch {
	case errors.Is(idErr, os.ErrNotExist):
		// 没有身份记录：要么没启动过，要么记录丢了。日志能区分这两件事。
		if ev.Journal.Records > 0 {
			ev.Reason = "日志里已有记录，但进程身份记录缺失：启动过、身份丢了，不能判定结束"
			return ev, nil
		}
		ev.State = StateNotStarted
		ev.Reason = "没有进程身份记录，日志也没有记录：从未启动过"
		return ev, nil
	case idErr != nil:
		ev.Reason = "进程身份记录损坏：" + idErr.Error()
		return ev, nil
	}

	ev.Identity = &id
	if opts.ExpectedNonce != "" && id.Nonce != opts.ExpectedNonce {
		ev.Reason = "身份记录的 nonce 不属于本次 Run：可能是别的运行的记录被挪过来了"
		return ev, nil
	}

	probe := opts.Probe
	if probe == nil {
		probe = processProber{}
	}
	stop, err := probe.Recover(id)
	if err != nil {
		ev.Reason = "无法核对进程身份：" + err.Error()
		return ev, nil
	}
	ev.Stop = &stop

	switch {
	case stop.Outcome == process.OutcomeExited && stop.GroupEmpty:
		ev.State = StateFinished
		ev.Reason = "进程已退出，且进程组已确认清空"
	case !stop.Exited:
		// 进程本身还在：监督方丢了，进程没丢。
		ev.State = StateRunning
		ev.Reason = "进程仍在运行：" + stop.Note
	default:
		ev.State = StateUnknown
		ev.Reason = "无法确认现场：" + stop.Note
	}
	return ev, nil
}

// readJournal 读出日志的可观察事实与游标；打不开就退化为空状态并写明原因。
func readJournal(opts Options) (journal.Status, journal.Cursor) {
	jr, err := journal.Open(opts.journalDir(), journal.Options{RunID: opts.RunID})
	if err != nil {
		return journal.Status{Incomplete: true}, journal.Cursor{}
	}
	defer jr.Close()
	return jr.Status(), jr.Cursor()
}

// Describe 是给看板用的一行摘要。
func (e Evidence) Describe() string {
	return fmt.Sprintf("%s：%s（日志 %d 条，待补交 %d 条）",
		e.State, e.Reason, e.Journal.Records, e.Journal.PendingRecords)
}
