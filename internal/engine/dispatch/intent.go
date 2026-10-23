package dispatch

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// Progress 是派发意图已经落盘的步骤。状态只能按以下顺序前进：
//
//	queued → preparing → launching → confirmed
//	queued/preparing ─→ withdrawn
//	preparing/launching ─→ failed
//
// confirmed、failed、withdrawn 是终态；uncertain 不是独立状态，而是 launching
// 上附加的「结果待核实」标记，避免恢复时把未确认的启动当成未发生。
type Progress string

const (
	// ProgressQueued 表示操作已排队、尚未开始准备。
	ProgressQueued Progress = "queued"
	// ProgressPreparing 表示准备工作已经开始，可安全重放。
	ProgressPreparing Progress = "preparing"
	// ProgressLaunching 表示启动意图已落盘，工具进程可能已经创建。
	ProgressLaunching Progress = "launching"
	// ProgressConfirmed 表示已经拿到并记录了进程身份，启动事实可恢复。
	ProgressConfirmed Progress = "confirmed"
	// ProgressFailed 表示准备或启动已确定性失败。
	ProgressFailed Progress = "failed"
	// ProgressWithdrawn 表示排队中的意图被撤回，未发生启动。
	ProgressWithdrawn Progress = "withdrawn"
)

// Valid 报告进度值是否属于已知步骤。
func (p Progress) Valid() bool {
	switch p {
	case ProgressQueued, ProgressPreparing, ProgressLaunching, ProgressConfirmed, ProgressFailed, ProgressWithdrawn:
		return true
	}
	return false
}

// Terminal 报告该进度是否已经结束，不再需要推进。
func (p Progress) Terminal() bool {
	return p == ProgressConfirmed || p == ProgressFailed || p == ProgressWithdrawn
}

// CanTransition 校验步骤是否允许从 from 前进到 to。
func CanTransition(from, to Progress) bool {
	switch from {
	case ProgressQueued:
		return to == ProgressPreparing || to == ProgressWithdrawn
	case ProgressPreparing:
		return to == ProgressLaunching || to == ProgressWithdrawn || to == ProgressFailed
	case ProgressLaunching:
		return to == ProgressConfirmed || to == ProgressFailed
	}
	return false
}

// ProcessIdentity 是一次工具进程启动的可核对身份。
//
// 字段只保存恢复所必需的事实：pid／pgid 用来确认「还是不是同一个进程」，
// started_at 用来排除 pid 复用，nonce 用来关联意图与启动日志。
type ProcessIdentity struct {
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid,omitempty"`
	Nonce     string    `json:"nonce,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

// Valid 报告身份是否足以在恢复时核对。
func (p ProcessIdentity) Valid() bool {
	return p.PID > 0
}

// Intent 是一次派发操作在本机的可恢复意图。
//
// 它不替代中枢里的 Operation 事实：Operation 仍是权威，Intent 只是「外部副作用
// 可能已经发生」的本地证据。恢复时先读 Intent，再决定是否可以重新执行。
type Intent struct {
	OperationID   model.OperationID `json:"operation_id"`
	ProjectID     model.ProjectID   `json:"project_id,omitempty"`
	RunID         model.RunID       `json:"run_id,omitempty"`
	Instance      model.InstanceID  `json:"task_instance,omitempty"`
	Generation    model.Generation  `json:"generation,omitempty"`
	RequestDigest string            `json:"request_digest,omitempty"`
	Progress      Progress          `json:"progress"`
	Uncertain     bool              `json:"uncertain,omitempty"`
	Nonce         string            `json:"nonce,omitempty"`
	SpecDigest    string            `json:"spec_digest,omitempty"`
	Identity      *ProcessIdentity  `json:"identity,omitempty"`
	Failure       string            `json:"failure,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	Transitions   []Progress        `json:"transitions,omitempty"`
}

// Clone 返回深拷贝，避免调用方改写存储中的意图。
func (i Intent) Clone() Intent {
	out := i
	if i.Identity != nil {
		id := *i.Identity
		out.Identity = &id
	}
	if len(i.Transitions) > 0 {
		out.Transitions = append([]Progress(nil), i.Transitions...)
	}
	return out
}

// Validate 校验意图是否自洽。
func (i Intent) Validate() error {
	if !i.OperationID.Valid() {
		return fmt.Errorf("dispatch: operation_id %q 形态非法", i.OperationID)
	}
	if !i.Progress.Valid() {
		return fmt.Errorf("dispatch: 操作 %s 的进度 %q 未知", i.OperationID, i.Progress)
	}
	if i.RunID != "" && !i.RunID.Valid() {
		return fmt.Errorf("dispatch: run_id %q 形态非法", i.RunID)
	}
	if i.Instance != "" && !i.Instance.Valid() {
		return fmt.Errorf("dispatch: task_instance %q 形态非法", i.Instance)
	}
	if i.Generation < 0 {
		return fmt.Errorf("dispatch: generation %d 不能为负", i.Generation)
	}
	if i.Progress == ProgressLaunching && i.Nonce == "" {
		return fmt.Errorf("dispatch: 操作 %s 已进入 launching 但缺少启动 nonce", i.OperationID)
	}
	if i.Progress == ProgressConfirmed && (i.Identity == nil || !i.Identity.Valid()) {
		return fmt.Errorf("dispatch: 操作 %s 已确认但没有可核对的进程身份", i.OperationID)
	}
	return nil
}

// IntentStore 是意图的持久化端口。实现必须做到「后写覆盖先写且不产生半个文件」。
type IntentStore interface {
	// Load 读取一个操作的意图；不存在时返回 (nil, nil)。
	Load(id model.OperationID) (*Intent, error)
	// Save 原子写入意图。
	Save(intent *Intent) error
	// List 按更新时间升序返回全部意图；目录不存在时返回空列表。
	List() ([]*Intent, error)
}

// ErrTooLate 表示意图已经越过启动点，不能再撤回。
var ErrTooLate = errors.New("dispatch: 意图已进入启动阶段，不能撤回")

// safeOpFile 把操作身份限制在固定字符集内，防止拼出目录穿越路径。
func safeOpFile(id model.OperationID) (string, error) {
	s := string(id)
	if !id.Valid() || strings.ContainsAny(s, `/\`) || s == "." || s == ".." {
		return "", fmt.Errorf("dispatch: operation_id %q 不能用作意图文件名", s)
	}
	return s + ".json", nil
}

// sortIntents 保证 List 结果稳定，便于恢复顺序可复现。
func sortIntents(items []*Intent) {
	sort.Slice(items, func(a, b int) bool {
		if items[a].UpdatedAt.Equal(items[b].UpdatedAt) {
			return items[a].OperationID < items[b].OperationID
		}
		return items[a].UpdatedAt.Before(items[b].UpdatedAt)
	})
}

// intentDir 返回状态目录下的意图目录。
func intentDir(stateDir string) string {
	return filepath.Join(stateDir, "dispatch", "intents")
}
