package app

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/matching"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/runner/process"
)

// 自动执行（工作台开关）的状态与用例。
//
// 产品口径只有一句：**打开它 = 让中枢用这个执行节点自动领题并开跑**。
// 底下的实现是三条配置字段的组合：
//
//	managed.auto_enabled     推进派发队列（消费 dispatch 操作）
//	managed.auto_select      生产 dispatch_ready（真正「领题」的那一步）
//	managed.allowed_nodes    只允许这个节点接单
//	system_agent.worker_node 自愈重跑时也用同一个节点
//
// 之前工作台只暴露了 auto_enabled，于是「打开」看起来成功、实际没有任何
// 任务被领走——原因是 auto_select 没有写入口。这个用例把三者绑成一次原子
// 写入，并且拒绝「节点根本接不了活」的开启请求，不再让用户以为是 bug。

// AutoExecutionNode 是一个候选执行节点及其就绪结论。
type AutoExecutionNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Ready 为真表示节点自身可接新任务。
	Ready bool `json:"ready"`
	// Reason 是未就绪的稳定原因码（见 matching 的 ReasonCode），就绪时为空。
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Slots 是当前空闲槽位：节点在接单，但没有空位时仍算「可用但忙」。
	Slots int `json:"slots"`
}

// AutoExecutionView 是工作台「自动执行」开关的完整状态。
type AutoExecutionView struct {
	ProjectID string `json:"project_id"`
	Enabled   bool   `json:"enabled"`
	// NodeID 是当前生效的执行节点；空表示「由匹配层按优先级挑」。
	NodeID string `json:"node_id"`
	// Node 是当前生效节点的就绪结论（NodeID 为空时指首选节点）。
	Node AutoExecutionNode `json:"node"`
	// Nodes 是本工程登记的全部候选节点，供下拉框使用。
	Nodes []AutoExecutionNode `json:"nodes"`
	Limit int                 `json:"limit"`
	// Ready 为真表示「现在打开真的会动」：有可接单节点且上限足够。
	Ready bool `json:"ready"`
	// Reasons 是未就绪的稳定原因码列表。
	Reasons []string `json:"reasons,omitempty"`
	// ServeRunning 报告常驻中枢是否在跑：不在跑时写盘成功也不会立刻执行。
	ServeRunning bool `json:"serve_running"`
	// Applied 为真表示写入会（或已经）立即生效。
	Applied bool `json:"applied"`
	// Reason 是「为什么没立刻生效」的稳定原因码。
	Reason string `json:"reason,omitempty"`
	// Detail 是原因码的人话补充（如节点探测失败的具体错误）。
	Detail string `json:"detail,omitempty"`
	// LastAttempt 是最近一次自动派发尝试（成功或跳过）的事实。
	LastAttempt map[string]any `json:"last_attempt,omitempty"`
}

// 自动执行未就绪 / 未生效的原因码（封闭集合，供界面与测试断言）。
const (
	AutoReasonServeNotRunning = "serve_not_running"
	AutoReasonNoNode          = "no_candidate_node"
	AutoReasonNodeStale       = "node_stale"
	AutoReasonNodeNotReady    = "node_not_ready"
	AutoReasonPlatform        = "platform_unsupported"
	AutoReasonLimit           = "managed_execution_limit"
	AutoReasonNoReadyTask     = "no_ready_task"
	AutoReasonApplied         = "applied"
)

// AutoExecutionInput 是开关写入请求。
//
// Enabled 必填；NodeID 为空或 "auto" 表示「不指定，交给匹配层挑」。
type AutoExecutionInput struct {
	ProjectID string
	Enabled   bool
	NodeID    string
	Actor     string
}

// AutoExecutionGet 读取工程当前的自动执行状态。
func AutoExecutionGet(projectID string) (AutoExecutionView, error) {
	pc, err := loadProjectCtx(projectID)
	if err != nil {
		return AutoExecutionView{}, err
	}
	return buildAutoExecutionView(pc, "")
}

// AutoExecutionSet 打开／关闭自动执行。
//
// 打开时校验节点真的能接活（缺证据就先探测一次），再一次性写齐四个字段；
// 关闭时只收两个开关，保留节点与上限，方便下次原样打开。
func AutoExecutionSet(in AutoExecutionInput) (AutoExecutionView, error) {
	pc, err := loadProjectCtx(in.ProjectID)
	if err != nil {
		return AutoExecutionView{}, err
	}
	cfgPath := home.ConfigPath(pc.LocalHome)
	if _, err := config.EnsureManagedSection(cfgPath); err != nil {
		return AutoExecutionView{}, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return AutoExecutionView{}, err
	}
	pc.Config = cfg

	wantNode := normalizeNodeChoice(in.NodeID)
	if !in.Enabled {
		// 关闭：只收两个开关。节点与上限保留，下次打开不用重选。
		if err := writeAutoExecution(cfgPath, cfg, false, cfg.Managed.AllowedNodes, ""); err != nil {
			return AutoExecutionView{}, err
		}
		writeAutoExecutionEvent(pc, in.Actor, false, "")
		reloadProjConfig(pc, cfgPath)
		return buildAutoExecutionView(pc, "")
	}

	// 打开：先确定用哪个节点。
	target, reason, detail, err := pickAutoExecutionNode(pc, wantNode)
	if err != nil {
		return AutoExecutionView{}, err
	}
	if target.ID == "" {
		view, verr := buildAutoExecutionView(pc, reason)
		if verr != nil {
			return AutoExecutionView{}, verr
		}
		view.Detail = detail
		view.Applied = false
		return view, nil
	}
	// 节点此刻接不了活：拒绝开启，而不是写一个「开了也不会动」的配置。
	if gate := matching.NodeGate(target.node, time.Now().UTC(), matching.DefaultEvidenceTTL); gate != nil {
		if gate.Code != matching.ReasonNodeOccupied {
			view, verr := buildAutoExecutionView(pc, string(gate.Code))
			if verr != nil {
				return AutoExecutionView{}, verr
			}
			view.Detail = gate.Detail
			view.Applied = false
			return view, nil
		}
		// 槽位占满只是「忙」，不是「不能接」：允许开启，等空槽自然接单。
	}
	allowed := []string{string(target.ID)}
	if err := writeAutoExecution(cfgPath, cfg, true, allowed, string(target.ID)); err != nil {
		return AutoExecutionView{}, err
	}
	// 写事件：开关的每次翻转都要能在时间轴上查到是谁、把哪个节点设成了自动执行。
	writeAutoExecutionEvent(pc, in.Actor, true, string(target.ID))
	reloadProjConfig(pc, cfgPath)
	return buildAutoExecutionView(pc, "")
}

// reloadProjConfig 把刚写下的配置读回内存视图。
//
// 必须重读：视图是「写完之后的现场」，拿写之前的快照去渲染，会让界面在
// 成功之后立刻显示回旧状态——用户只会理解成「点不动」。
func reloadProjConfig(pc *projCtx, cfgPath string) {
	if next, err := config.Load(cfgPath); err == nil {
		pc.Config = next
	}
}

// writeAutoExecution 把开关组合写回 config.toml。
//
// 关闭时不覆盖 allowed_nodes 与 worker_node：保留现场，避免「关一次就得重配」。
func writeAutoExecution(cfgPath string, cfg *config.Config, enabled bool, allowed []string, workerNode string) error {
	next := *cfg
	next.Managed.AutoEnabled = enabled
	next.Managed.AutoSelect = enabled
	if enabled {
		if next.Managed.Limit < 1 {
			next.Managed.Limit = 1
		}
		next.Managed.AllowedNodes = append([]string{}, allowed...)
		next.SystemAgent.WorkerNode = workerNode
	}
	if err := writeProjectConfig(cfgPath, &next); err != nil {
		return err
	}
	return nil
}

// nodeChoice 是一次候选节点解析的结果。
type nodeChoice struct {
	ID   model.NodeID
	node matching.Node
}

func normalizeNodeChoice(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "auto" {
		return ""
	}
	return raw
}

// pickAutoExecutionNode 解析要用的节点：显式指定则用它，否则取第一个可接单的。
//
// 缺证据（node_stale）时会先同步探测一次再判：否则「刚登记、还没跑 serve」
// 的节点永远开不起来，而这正是最常见的第一天场景。
func pickAutoExecutionNode(pc *projCtx, want string) (nodeChoice, string, string, error) {
	nodes, err := projectNodes(pc)
	if err != nil {
		return nodeChoice{}, "", "", err
	}
	if len(nodes) == 0 {
		return nodeChoice{}, AutoReasonNoNode, "", nil
	}
	if want != "" {
		for _, n := range nodes {
			if string(n.ID) != want {
				continue
			}
			if matching.NodeGate(n.node, time.Now().UTC(), matching.DefaultEvidenceTTL) == nil {
				return nodeChoice{ID: n.ID, node: n.node}, "", "", nil
			}
			// 先探测再判一次：观测过期是最好修的一种「不可用」。
			if err := probeAndReload(pc, n.ID); err != nil {
				return nodeChoice{ID: n.ID, node: n.node}, AutoReasonNodeStale, err.Error(), nil
			}
			nodes, err = projectNodes(pc)
			if err != nil {
				return nodeChoice{}, "", "", err
			}
			for _, m := range nodes {
				if m.ID == n.ID {
					if gate := matching.NodeGate(m.node, time.Now().UTC(), matching.DefaultEvidenceTTL); gate == nil {
						return nodeChoice{ID: m.ID, node: m.node}, "", "", nil
					} else {
						return nodeChoice{ID: m.ID, node: m.node}, string(gate.Code), gate.Detail, nil
					}
				}
			}
			return nodeChoice{}, AutoReasonNoNode, "", nil
		}
		// 指名的节点不存在：明确报错，不要静默换成别的节点。
		return nodeChoice{}, AutoReasonNoNode, "节点 " + want + " 未在本工程登记", nil
	}
	// 未指定：优先挑已经就绪的，其次挑「修一下就能用」的（观测过期）。
	var stale *nodeChoice
	for _, n := range nodes {
		gate := matching.NodeGate(n.node, time.Now().UTC(), matching.DefaultEvidenceTTL)
		if gate == nil {
			return nodeChoice{ID: n.ID, node: n.node}, "", "", nil
		}
		if stale == nil && gate.Code == matching.ReasonNodeStale {
			c := nodeChoice{ID: n.ID, node: n.node}
			stale = &c
		}
	}
	if stale != nil {
		if err := probeAndReload(pc, stale.ID); err == nil {
			if nodes2, e := projectNodes(pc); e == nil {
				for _, m := range nodes2 {
					if m.ID == stale.ID && matching.NodeGate(m.node, time.Now().UTC(), matching.DefaultEvidenceTTL) == nil {
						return nodeChoice{ID: m.ID, node: m.node}, "", "", nil
					}
				}
			}
		}
		return nodeChoice{}, AutoReasonNodeStale, "登记节点的观测证据已过期", nil
	}
	// 一个都没就绪：把第一个节点的原因带出去，让界面能解释。
	first := nodes[0]
	gate := matching.NodeGate(first.node, time.Now().UTC(), matching.DefaultEvidenceTTL)
	if gate == nil {
		return nodeChoice{ID: first.ID, node: first.node}, "", "", nil
	}
	return nodeChoice{}, string(gate.Code), gate.Detail, nil
}

// probeAndReload 同步探测一个节点并回写观测证据。
func probeAndReload(pc *projCtx, nodeID model.NodeID) error {
	if err := probeNodeNow(pc.GlobalDB, string(nodeID)); err != nil {
		return err
	}
	return nil
}

// nodeRow 是节点行 + 匹配层投影的组合，避免反复转换。
type nodeRow struct {
	ID    model.NodeID
	Label string
	node  matching.Node
}

func projectNodes(pc *projCtx) ([]nodeRow, error) {
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	var out []nodeRow
	err = store.Read(func(tx *sql.Tx) error {
		rows, e := db.ListNode(tx, pc.ID())
		if e != nil {
			return e
		}
		for _, n := range rows {
			cap, e := db.Capacity(tx, n.ID)
			if e != nil {
				return e
			}
			out = append(out, nodeRow{ID: model.NodeID(n.ID), Label: n.Label, node: matchingNode(n, cap.ActiveSlots)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// buildAutoExecutionView 组装状态视图。extraReason 用于把「本次被拒的原因」带出去。
func buildAutoExecutionView(pc *projCtx, extraReason string) (AutoExecutionView, error) {
	cfg := pc.Config
	view := AutoExecutionView{ProjectID: pc.ID()}
	if cfg != nil {
		view.Enabled = cfg.Managed.AutoEnabled && cfg.Managed.AutoSelect
		view.Limit = cfg.Managed.Limit
		if view.Limit < 1 {
			view.Limit = 1
		}
		if len(cfg.Managed.AllowedNodes) > 0 {
			view.NodeID = strings.TrimSpace(cfg.Managed.AllowedNodes[0])
		}
	} else {
		view.Limit = 1
	}

	rows, err := projectNodes(pc)
	if err != nil {
		return AutoExecutionView{}, err
	}
	now := time.Now().UTC()
	view.Nodes = make([]AutoExecutionNode, 0, len(rows))
	for _, r := range rows {
		gate := matching.NodeGate(r.node, now, matching.DefaultEvidenceTTL)
		item := AutoExecutionNode{ID: string(r.ID), Label: r.Label, Ready: gate == nil}
		if gate != nil {
			item.Reason = string(gate.Code)
			item.Detail = gate.Detail
		}
		if free := r.node.Config.SlotLimit - r.node.ActiveSlots; free > 0 {
			item.Slots = free
		}
		view.Nodes = append(view.Nodes, item)
		if view.NodeID == "" && gate == nil && item.Ready {
			view.NodeID = string(r.ID)
		}
	}
	for _, item := range view.Nodes {
		if item.ID == view.NodeID {
			view.Node = item
			break
		}
	}

	// 就绪判定：平台、节点、上限三项都要成立，否则「开了也不会动」。
	var reasons []string
	if !platformSupported() {
		reasons = append(reasons, AutoReasonPlatform)
	}
	if len(rows) == 0 {
		reasons = append(reasons, AutoReasonNoNode)
	} else if !view.Node.Ready {
		if view.Node.Reason != "" {
			reasons = append(reasons, view.Node.Reason)
		} else {
			reasons = append(reasons, AutoReasonNodeNotReady)
		}
	}
	if view.Limit < 1 {
		reasons = append(reasons, AutoReasonLimit)
	}
	if extraReason != "" && !containsString(reasons, extraReason) {
		reasons = append(reasons, extraReason)
	}
	view.Ready = len(reasons) == 0
	view.Reasons = reasons

	view.ServeRunning = leaderRunning(pc)
	view.Applied = view.ServeRunning
	if !view.ServeRunning {
		view.Reason = AutoReasonServeNotRunning
	}
	view.LastAttempt = lastAutoDispatchAttempt(pc)
	return view, nil
}

// platformSupported 报告本机是否具备受管执行能力（与 ManagedView 同一口径）。
func platformSupported() bool { return runtime.GOOS == "darwin" }

// stateDirFor 返回工程的受管运行状态目录（与 Runtime.StateDir 同构）。
func stateDirFor(pc *projCtx) string {
	global, err := home.GlobalDir()
	if err != nil {
		return ""
	}
	return filepath.Join(global, "runtime", pc.ID())
}

// leaderRunning 用协调者锁判定常驻 leader 是否在跑。
//
// 判据只有一条：这把锁能不能抢到。抢不到就是有人持锁（leader 在跑）；
// 抢到说明没人跑，立刻释放，不留副作用。
func leaderRunning(pc *projCtx) bool {
	if pc == nil {
		return false
	}
	lockPath := filepath.Join(stateDirFor(pc), "dispatch", "coordinator.lock")
	lock, err := process.AcquireLock(lockPath)
	if err != nil {
		if errors.Is(err, process.ErrUnsupported) {
			return false
		}
		return true
	}
	_ = lock.Release()
	return false
}

// lastAutoDispatchAttempt 取最近一次自动派发相关事件的摘要。
func lastAutoDispatchAttempt(pc *projCtx) map[string]any {
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil
	}
	defer store.Close()
	var out map[string]any
	_ = store.Read(func(tx *sql.Tx) error {
		events, e := db.ListEventsSince(tx, 0, pc.ID(), "", 200)
		if e != nil {
			return e
		}
		for i := len(events) - 1; i >= 0; i-- {
			ev := events[i]
			switch ev.Type {
			case "auto_execution_changed", "dispatch_skipped", "dispatch_started", leadLoopErrorEvent:
			default:
				continue
			}
			out = map[string]any{"type": ev.Type, "at": ev.TS, "data": ev.Data}
			return nil
		}
		return nil
	})
	return out
}

// writeAutoExecutionEvent 把开关变更落成可查询事实。
//
// 无人值守执行是重决定，必须留下「谁在什么时候打开了哪个节点」的证据；
// 写失败不影响已经生效的配置，但会被上层记进日志。
func writeAutoExecutionEvent(pc *projCtx, actor string, enabled bool, nodeID string) {
	if pc == nil {
		return
	}
	if strings.TrimSpace(actor) == "" {
		actor = "user"
	}
	_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		return db.AppendEvent(tx, pc.ID(), actor, "auto_execution_changed", "", map[string]any{
			"enabled": enabled,
			"node_id": nodeID,
		})
	})
}

// loadProjectCtx 按工程 ID 解析出可写上下文（含 config）。
func loadProjectCtx(projectID string) (*projCtx, error) {
	projectID = strings.TrimSpace(projectID)
	if !model.ProjectID(projectID).Valid() {
		return nil, fmt.Errorf("工程 ID 非法：%q", projectID)
	}
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	var row *db.Project
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		p, e := db.GetProject(tx, projectID)
		if e != nil {
			return e
		}
		row = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("工程 %s 未登记", projectID)
	}
	pc, err := resolveProject(row.Path)
	if err != nil {
		return nil, err
	}
	cfgPath := home.ConfigPath(pc.LocalHome)
	if _, err := os.Stat(cfgPath); err == nil {
		if cfg, e := config.Load(cfgPath); e == nil {
			pc.Config = cfg
		}
	}
	return pc, nil
}
