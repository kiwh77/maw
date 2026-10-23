package workspace

import (
	"context"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// HoldState 是目录占用的现状分类（maw.runtime.state/v1 §4.1）。
//
// 「仍然占用」不是只看阶段字符串：unknown 与进程未确认停止都继续计入占用，
// 否则一份没核实清楚的运行会被当成空闲，下一题直接把它重置掉。
type HoldState string

const (
	// HoldActive 是运行尚未进入终态的占用。
	HoldActive HoldState = "active"
	// HoldUnknown 是核实状态 unknown 的占用：证据不足，保持现场。
	HoldUnknown HoldState = "unknown"
	// HoldStopUnconfirmed 是阶段已失败／取消但进程未确认停止的占用。
	HoldStopUnconfirmed HoldState = "stop_unconfirmed"
	// HoldPendingAcceptance 是运行已结束但产物未冻结的占用：目录里可能还躺着待验收的提交。
	HoldPendingAcceptance HoldState = "pending_acceptance"
	// HoldFrozen 是运行已结束且产物已冻结：目录可以复用。
	HoldFrozen HoldState = "frozen"
)

// Blocks 报告该占用状态是否阻止复用目录。
func (s HoldState) Blocks() bool { return s != HoldFrozen && s != "" }

// Title 返回给人看的短说明。
func (s HoldState) Title() string {
	switch s {
	case HoldActive:
		return "运行进行中"
	case HoldUnknown:
		return "核实状态未知"
	case HoldStopUnconfirmed:
		return "进程停止未确认"
	case HoldPendingAcceptance:
		return "产物待验收"
	case HoldFrozen:
		return "产物已冻结"
	}
	return "未知占用"
}

// Occupant 是某个目录上的一条占用现场：谁持有、哪一代、处于什么状态。
//
// 判定复用 model.Run 的 Occupies()，不在这里另写一套阶段规则——
// 两份规则一旦漂移，就会出现「结构校验说空闲、准备却复用」的分裂。
type Occupant struct {
	Instance model.InstanceID `json:"task_instance"`
	Run      model.Run        `json:"run"`
}

// State 按 §4.1 给占用分类。
func (o Occupant) State() HoldState {
	if o.Run.Occupies() {
		switch {
		case o.Run.Reconciliation == model.ReconciliationUnknown:
			return HoldUnknown
		case (o.Run.Phase == model.PhaseFailed || o.Run.Phase == model.PhaseCancelled) && !o.Run.ProcessStopConfirmed:
			return HoldStopUnconfirmed
		default:
			return HoldActive
		}
	}
	if !o.Run.DeliveryFrozen {
		// 运行结束了但产物没冻结：目录里的提交还没验收、没合入，复用等于把它冲掉（E242）。
		return HoldPendingAcceptance
	}
	return HoldFrozen
}

// Blocks 报告该占用是否阻止复用。
func (o Occupant) Blocks() bool { return o.State().Blocks() }

// Describe 给出一行可核对的占用描述。
func (o Occupant) Describe() string {
	parts := []string{o.State().Title()}
	if o.Instance != "" {
		parts = append(parts, "task_instance="+string(o.Instance))
	}
	if o.Run.RunID != "" {
		parts = append(parts, "run="+string(o.Run.RunID))
	}
	if o.Run.Generation.Valid() {
		parts = append(parts, "gen="+strconv.Itoa(int(o.Run.Generation)))
	}
	if o.Run.Phase != "" {
		parts = append(parts, "phase="+string(o.Run.Phase))
	}
	return strings.Join(parts, " ")
}

// Index 是工作空间对占用记录的只读视图。
//
// 实现方（app 层）用 internal/db 落地：先 db.Resource{Kind: model.LeaseWorkspace,
// Path: normalized}.Key() 得到规范化资源键，再读 resource_leases 与对应 Run。
// 本包不 import internal/db：工作空间只需要「这个目录现在归谁」，不需要数据库。
type Index interface {
	// WorkspaceOccupant 返回该规范化目录上的有效占用；没有返回 (nil, nil)。
	WorkspaceOccupant(ctx context.Context, normalizedPath string) (*Occupant, error)
}

// IndexFunc 把函数适配成 Index，便于装配与测试。
type IndexFunc func(ctx context.Context, normalizedPath string) (*Occupant, error)

// WorkspaceOccupant 实现 Index。
func (f IndexFunc) WorkspaceOccupant(ctx context.Context, normalizedPath string) (*Occupant, error) {
	return f(ctx, normalizedPath)
}

// FreeIndex 表示「没有占用记录」的索引：只在确认过没有任何运行占用时使用。
func FreeIndex() Index {
	return IndexFunc(func(context.Context, string) (*Occupant, error) { return nil, nil })
}

// occupancyConflict 把占用写成 E240：同一资源已有有效持有者。
func occupancyConflict(path string, occ *Occupant) error {
	return model.Violationf(model.CodeDoubleOccupancy, "workspace.path",
		"%s 仍被占用（%s）：产物冻结／验收与合入结束前不得复用，也不能新建同路径目录",
		path, occ.Describe())
}
