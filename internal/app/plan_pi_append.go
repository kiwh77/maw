package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kiwh77/maw/internal/blueprint"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/goal"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/identity"
)

// 本文件是「已批准计划修订 → 蓝图追加 → 增量 apply → 任务映射」的唯一入口
// （契约 maw.lead.goal/v1 §8）。三类副作用各只发生一次：
//
//	文件   AppendTasks 只追加，绝不重写；候选全文先校验后落盘。
//	数据库 台账 appended 先于写盘落库，applied 与映射在 apply 成功后同事务写。
//	重放   同一 enqueue_key 再来一次只返回原映射，不追加、不建题。
//
// CLI 入口属于 PI-303，本文件只提供可被 CLI／测试直接调用的库函数。

// PiAppendInput 是一次入队请求。
type PiAppendInput struct {
	// Cwd 用于定位工程（任意子目录均可）。
	Cwd string
	// Plan 是已经批准的计划修订，必须带 plan_digest 与 approval。
	Plan goal.PlanRevision
	// BlueprintPath 留空时取工程本地 home 下的蓝图。
	BlueprintPath string
	// DryRun 只做校验与预览，不写文件、不写库。
	DryRun bool
	// Actor 留空时按当前身份解析。
	Actor string
}

// piCardDecision 是单张卡在这次入队里的处置。
type piCardDecision struct {
	Card goal.PlanCard
	Key  string
	// State 取 append（需要追加）、present（已在蓝图里）、applied（已入队）。
	State string
}

const (
	piCardAppend  = "append"
	piCardPresent = "present"
	piCardApplied = "applied"
)

// PiAppendPlan 执行一次「批准计划 → 入队」的完整流程，并返回可核对的证据。
//
// 顺序固定（契约 §8.2）：校验批准 → 读基线 → 内存校验合并后的全文 → 原子追加
// → 增量 apply + 映射/台账。任何一步失败都不留下半套状态：文件没写就是没写，
// 写了但 apply 失败时台账停在 appended，重跑同一请求会从第 5 步继续。
func PiAppendPlan(in PiAppendInput) (Result, error) {
	pc, err := resolveProject(in.Cwd)
	if err != nil {
		return nil, err
	}
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = "orchestrator"
		if who, err := identity.Resolve(in.Cwd, ""); err == nil {
			actor = who
		}
	}
	plan := in.Plan
	bpPath := strings.TrimSpace(in.BlueprintPath)
	if bpPath == "" {
		bpPath = home.BlueprintPath(pc.LocalHome)
	}

	// 1. 批准与摘要：没有 approved + 匹配摘要，一条卡也不写（G202／G304）。
	if err := plan.EnsureDigest(); err != nil {
		return nil, err
	}
	if vs := plan.Validate(); len(vs) > 0 {
		return nil, vs.Err()
	}
	if plan.Status != goal.PlanApproved {
		return nil, fmt.Errorf("%w: rev %d 当前 %s", errPiEnqueueNotApproved, plan.PlanRev, plan.Status)
	}
	if plan.Approval.State != goal.ApprovalApproved || strings.TrimSpace(plan.Approval.Approver) == "" {
		return nil, fmt.Errorf("plan rev %d 未批准：先 maw lead goal approve 再入队", plan.PlanRev)
	}
	if plan.ProjectID != "" && plan.ProjectID != pc.ID() {
		return nil, fmt.Errorf("计划修订属于工程 %s，与当前工程 %s 不一致", plan.ProjectID, pc.ID())
	}

	var (
		ledger map[string]*goal.EnqueueRecord
		links  map[string]*goal.TaskLink
	)
	keys := make([]string, 0, len(plan.Cards))
	for _, c := range plan.Cards {
		keys = append(keys, goal.EnqueueKey(pc.ID(), plan.GoalID, plan.PlanRev, plan.PlanDigest, c.CardID))
	}
	keyByCard := map[string]string{}
	for i, c := range plan.Cards {
		keyByCard[c.CardID] = keys[i]
	}

	// 2. 目标状态：暂停／升级／已完结的目标不得再入队（契约 §8 第 1 步）。
	if err := withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		g, err := db.GetGoal(tx, pc.ID(), plan.GoalID)
		if err != nil {
			return err
		}
		if g == nil {
			return fmt.Errorf("%w: %s", goal.ErrGoalNotFound, plan.GoalID)
		}
		switch g.Status {
		case goal.StatusPaused, goal.StatusEscalated, goal.StatusDone, goal.StatusAbandoned:
			return fmt.Errorf("目标 %s 当前状态 %s，不能再入队；先 resume 或换新目标", plan.GoalID, g.Status)
		}
		if plan.GoalVersion != g.ActiveVersion {
			return fmt.Errorf("计划修订绑定目标版本 %d，当前是 %d（目标已改版，按新版本重规划）",
				plan.GoalVersion, g.ActiveVersion)
		}
		ledger = map[string]*goal.EnqueueRecord{}
		links = map[string]*goal.TaskLink{}
		for _, key := range keys {
			rec, err := db.GetEnqueueRecord(tx, pc.ID(), key)
			if err != nil {
				return err
			}
			ledger[key] = rec
		}
		list, err := db.ListTaskLinks(tx, pc.ID(), plan.GoalID)
		if err != nil {
			return err
		}
		for _, l := range list {
			links[l.EnqueueKey] = l
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// 3. 读基线并计算每张卡的处置。
	orig, err := os.ReadFile(bpPath)
	if err != nil {
		return nil, fmt.Errorf("读取蓝图 %q 失败：%w", bpPath, err)
	}
	baseHash := sha256Hex(orig)

	existing, err := blueprint.Parse(orig)
	if err != nil {
		return nil, err
	}
	if errs := blueprint.Validate(existing); len(errs) > 0 {
		return nil, fmt.Errorf("既有蓝图不合法，拒绝在其上追加：%s", strings.Join(errs, "; "))
	}
	existingIDs := map[string]bool{}
	for _, t := range existing.Tasks {
		existingIDs[t.ID] = true
	}

	decisions := make([]piCardDecision, 0, len(plan.Cards))
	var toAppend []goal.PlanCard
	applied := 0
	for _, c := range plan.Cards {
		key := keyByCard[c.CardID]
		rec := ledger[key]
		_, present := existingIDs[c.ID]
		switch {
		case rec != nil && rec.State == goal.EnqueueApplied:
			if !present {
				return nil, fmt.Errorf("台账显示卡 %s 已 applied，但蓝图里没有 %s：先核对蓝图基线再重试", c.CardID, c.ID)
			}
			applied++
			decisions = append(decisions, piCardDecision{Card: c, Key: key, State: piCardApplied})
		case rec != nil && rec.State == goal.EnqueueAppended:
			state := piCardAppend
			if present {
				state = piCardPresent
			}
			decisions = append(decisions, piCardDecision{Card: c, Key: key, State: state})
			if !present {
				toAppend = append(toAppend, c)
			}
		default:
			if present {
				return nil, fmt.Errorf("%w: 卡 %s 的编号 %s 已在蓝图里，但台账没有对应记录（并发了别的追加？）",
					goal.ErrTaskLinkConflict, c.CardID, c.ID)
			}
			decisions = append(decisions, piCardDecision{Card: c, Key: key, State: piCardAppend})
			toAppend = append(toAppend, c)
		}
	}

	// 全部已入队：重放返回原映射，不产生任何写入（G301／G204）。
	if applied == len(plan.Cards) {
		return piReplayResult(pc.ID(), plan, keys, links), nil
	}

	fragment, err := renderPiCards(plan, toAppend)
	if err != nil {
		return nil, err
	}
	merged := orig
	if len(toAppend) > 0 {
		merged, err = MergeForAppend(orig, fragment)
		if err != nil {
			return nil, err
		}
	}

	// 4. 校验合并后的**完整**蓝图，并补上跨库检查（契约 §6.1 + §8.2 第 3 步）。
	if err := validateMergedBlueprint(pc, orig, merged, plan, toAppend); err != nil {
		return nil, err
	}
	mergedFile, err := blueprint.Parse(merged)
	if err != nil {
		return nil, err
	}
	warns := blueprint.ValidateLint(mergedFile)

	if in.DryRun {
		return Result{
			"ok": true, "dry_run": true, "project_id": pc.ID(), "goal_id": plan.GoalID,
			"plan_rev": plan.PlanRev, "plan_digest": plan.PlanDigest,
			"baseline_blueprint_sha256": baseHash, "enqueue_keys": keys,
			"append": len(toAppend), "already_present": len(plan.Cards) - len(toAppend) - applied,
			"warnings": warns, "blueprint": bpPath,
			"next": "maw lead goal enqueue --rev " + fmt.Sprint(plan.PlanRev) + "（审批后才会落盘）",
		}, nil
	}

	// 5. 台账先落 appended：崩溃在写盘前也能凭它重跑第 4–6 步。
	if len(toAppend) > 0 {
		if err := withGlobalWrite(pc.GlobalDB, func(tx *sql.Tx) error {
			for _, c := range toAppend {
				key := keyByCard[c.CardID]
				if _, _, err := db.RecordEnqueue(tx, &goal.EnqueueRecord{
					ProjectID: pc.ID(), EnqueueKey: key, GoalID: plan.GoalID,
					PlanRev: plan.PlanRev, CardID: c.CardID, PlanDigest: plan.PlanDigest,
					State: goal.EnqueueAppended,
				}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}

		// 6. 原子追加；写前再核一次基线，基线与校验时不一致就整体重来（并发保护）。
		cur, err := os.ReadFile(bpPath)
		if err != nil {
			return nil, fmt.Errorf("追加前重读蓝图失败：%w", err)
		}
		if sha256Hex(cur) != baseHash {
			return nil, fmt.Errorf("蓝图在校验与写入之间被改动（基线 %s → %s），本次未追加；请重跑入队以基于新基线校验",
				baseHash[:12], sha256Hex(cur)[:12])
		}
		if _, err := AppendTasks(bpPath, fragment, false); err != nil {
			return nil, err
		}
	}

	// 7. 增量 apply：保留既有任务、活跃 Run 与实例版本（G305）。
	if _, err := PlanApply(bpPath, in.Cwd, true, false, false); err != nil {
		return nil, fmt.Errorf("蓝图已追加但增量 apply 失败：%w（重跑同一入队请求会从第 5 步继续，不会重复追加）", err)
	}

	// 8. 写映射与终态台账（同事务）：重放靠这两张表，不靠目录或内存。
	var linkRows []*goal.TaskLink
	if err := withGlobalWrite(pc.GlobalDB, func(tx *sql.Tx) error {
		for _, d := range decisions {
			if d.State != piCardAppend && d.State != piCardPresent {
				continue
			}
			task, err := db.GetTask(tx, pc.ID(), d.Card.ID)
			if err != nil {
				return err
			}
			if task == nil {
				return fmt.Errorf("增量 apply 后找不到任务 %s：拒绝写映射", d.Card.ID)
			}
			version, err := activeTaskVersion(tx, task)
			if err != nil {
				return err
			}
			link, _, err := db.RecordTaskLink(tx, &goal.TaskLink{
				ProjectID: pc.ID(), GoalID: plan.GoalID, PlanRev: plan.PlanRev,
				CardID: d.Card.CardID, InstanceID: task.InstanceID, DisplayID: task.ID,
				VersionNo: version, EnqueueKey: d.Key,
				State: goal.LinkApplied, AppliedBy: actor,
			})
			if err != nil {
				return err
			}
			linkRows = append(linkRows, link)
			if _, _, err := db.RecordEnqueue(tx, &goal.EnqueueRecord{
				ProjectID: pc.ID(), EnqueueKey: d.Key, GoalID: plan.GoalID,
				PlanRev: plan.PlanRev, CardID: d.Card.CardID, PlanDigest: plan.PlanDigest,
				State: goal.EnqueueApplied,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	out := Result{
		"ok": true, "project_id": pc.ID(), "goal_id": plan.GoalID,
		"plan_rev": plan.PlanRev, "plan_digest": plan.PlanDigest,
		"baseline_blueprint_sha256": baseHash, "current_blueprint_sha256": sha256Hex(mustRead(bpPath)),
		"enqueue_keys": keys, "appended": len(toAppend), "replayed": false,
		"warnings": warns, "links": linkRows, "blueprint": bpPath,
		"next": "maw lead goal status 复核映射与台账",
	}
	return out, nil
}

func piReplayResult(projectID string, plan goal.PlanRevision, keys []string, links map[string]*goal.TaskLink) Result {
	outLinks := make([]*goal.TaskLink, 0, len(keys))
	for _, key := range keys {
		if l := links[key]; l != nil {
			outLinks = append(outLinks, l)
		}
	}
	return Result{
		"ok": true, "replayed": true, "project_id": projectID, "goal_id": plan.GoalID,
		"plan_rev": plan.PlanRev, "plan_digest": plan.PlanDigest,
		"enqueue_keys": keys, "appended": 0, "links": outLinks,
		"next": "同一批准操作已入队过：返回原映射，未追加任何内容",
	}
}

// renderPiCards 把计划卡渲染成「只含 [[task]] 块」的追加片段。
//
// 字段顺序对着蓝图既有风格；Origin 以注释落盘，便于 grep 回溯到目标与修订，
// 注释行不会被 planAppend 当成表头。
func renderPiCards(plan goal.PlanRevision, cards []goal.PlanCard) ([]byte, error) {
	if len(cards) == 0 {
		return nil, nil
	}
	var b strings.Builder
	b.WriteString("# maw lead goal enqueue：目标 " + plan.GoalID + " / 修订 " + fmt.Sprint(plan.PlanRev) + " / 摘要 " + plan.PlanDigest[:12] + "\n")
	for _, c := range cards {
		if strings.TrimSpace(c.ID) == "" {
			return nil, fmt.Errorf("卡 %s 缺蓝图编号，拒绝渲染", c.CardID)
		}
		b.WriteString("\n[[task]]\n")
		b.WriteString("id = " + quoteTOML(c.ID) + "\n")
		b.WriteString("title = " + quoteTOML(c.Title) + "\n")
		b.WriteString("goal = " + quoteTOML(c.Goal) + "\n")
		if c.Iteration != "" {
			b.WriteString("iteration = " + quoteTOML(c.Iteration) + "\n")
		}
		b.WriteString(fmt.Sprintf("priority = %d\n", c.Priority))
		b.WriteString(fmt.Sprintf("merge_order = %d\n", c.MergeOrder))
		b.WriteString("deps = " + arrayTOML(c.Deps) + "\n")
		b.WriteString("write_scope = " + arrayTOML(c.WriteScope) + "\n")
		b.WriteString("read_scope = " + arrayTOML(c.ReadScope) + "\n")
		b.WriteString("acceptance = " + arrayTOML(c.Acceptance) + "\n")
		b.WriteString("verify = " + arrayTOML(c.Verify) + "\n")
		b.WriteString("not_doing = " + arrayTOML(c.NotDoing) + "\n")
		fmt.Fprintf(&b, "# origin: goal=%s plan_rev=%d card=%s\n", plan.GoalID, plan.PlanRev, c.CardID)
		if len(c.Origin.SourceRefs) > 0 {
			fmt.Fprintf(&b, "# source: %s\n", strings.Join(c.Origin.SourceRefs, ", "))
		}
	}
	return []byte(b.String()), nil
}

// validateMergedBlueprint 校验「既有 + 片段」的合并结果，并补跨库检查。
//
// 只校验新增卡涉及的 id／依赖：既有任务的合法性不因一次追加被重新审判，
// 但新增卡不能踩到既有编号、不能依赖退役或取消的任务。
func validateMergedBlueprint(pc *projCtx, orig, merged []byte, plan goal.PlanRevision, added []goal.PlanCard) error {
	file, err := blueprint.Parse(merged)
	if err != nil {
		return err
	}
	if errs := blueprint.Validate(file); len(errs) > 0 {
		return fmt.Errorf("合并后的蓝图校验失败：%s", strings.Join(errs, "; "))
	}

	inPlan := map[string]goal.PlanCard{}
	for _, c := range plan.Cards {
		inPlan[c.ID] = c
	}
	// 片段与既有内容的 id 冲突由 AppendTasks 拦住；这里补「同一卡在既有蓝图里
	// 已有同名任务且不是本次追加」的语义检查。
	origFile, err := blueprint.Parse(orig)
	if err != nil {
		return err
	}
	origIDs := map[string]bool{}
	for _, t := range origFile.Tasks {
		origIDs[t.ID] = true
	}
	return withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		for _, c := range plan.Cards {
			for _, dep := range c.Deps {
				dep = strings.TrimSpace(dep)
				if dep == "" {
					continue
				}
				if _, ok := inPlan[dep]; ok {
					continue
				}
				task, err := db.GetTask(tx, pc.ID(), dep)
				if err != nil {
					return err
				}
				if task == nil {
					return fmt.Errorf("%s 依赖 %s：蓝图与任务表里都没有这道题，拒绝入队", c.ID, dep)
				}
				if task.Status == "cancelled" {
					return fmt.Errorf("%s 依赖 %s：前置已取消，依赖不可满足", c.ID, dep)
				}
				inst, err := db.GetInstance(tx, task.InstanceID)
				if err != nil {
					return err
				}
				if inst != nil && inst.State == model.InstanceStateRetired {
					return fmt.Errorf("%s 依赖 %s：前置实例已退役，依赖不可满足", c.ID, dep)
				}
			}

		}
		for _, c := range added {
			if origIDs[c.ID] {
				return fmt.Errorf("卡 %s 的编号 %s 已存在于蓝图，拒绝重复追加", c.CardID, c.ID)
			}
		}
		return nil
	})
}

// activeTaskVersion 取任务当前定义版本；取不到时退回 1（新题首发即 v1）。
func activeTaskVersion(tx *sql.Tx, task *db.Task) (int, error) {
	if task.InstanceID == "" {
		return 1, nil
	}
	inst, err := db.GetInstance(tx, task.InstanceID)
	if err != nil || inst == nil || inst.ActiveVersion == nil {
		return 1, nil
	}
	return int(*inst.ActiveVersion), nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mustRead(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}

// PiAppendKeys 计算一次修订的幂等键序列，供预览／核对使用。
func PiAppendKeys(projectID string, plan goal.PlanRevision) []string {
	keys := make([]string, 0, len(plan.Cards))
	for _, c := range plan.Cards {
		keys = append(keys, goal.EnqueueKey(projectID, plan.GoalID, plan.PlanRev, plan.PlanDigest, c.CardID))
	}
	sort.Strings(keys)
	return keys
}

var errPiEnqueueNotApproved = errors.New("plan revision is not approved")
