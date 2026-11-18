package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/goal"
)

// 本文件是「目标 → 规划 → 批准 → 入队」的 app 层入口（契约 maw.lead.goal/v1 §5–§8）。
//
// 三条边界：
//   - 规划只读：Planner 失败或计划校验失败时，任务、蓝图、目标状态都不动；
//   - 默认进人工审查：只有显式 auto_enqueue 策略才在同一次调用里入队；
//   - 入队复用 PI-302 的 PiAppendPlan，不在这里另写一套追加逻辑。

// LeadPlanner 产出结构化计划卡。生产实现是 Pi 运行时，测试可注入。
type LeadPlanner interface {
	Plan(ctx context.Context, req goal.PlanRequest) ([]goal.PlanCard, error)
}

// PiLeadPlanner 通过 Pi 运行时的 lead 模式生成计划。
//
// 运行时只给结构化输出：stdout 一行一个事件，result 事件的 text 里是计划 JSON。
type PiLeadPlanner struct {
	NodePath    string
	RuntimePath string
	Model       string
	Timeout     time.Duration
}

// Plan 实现 LeadPlanner。
func (p *PiLeadPlanner) Plan(ctx context.Context, req goal.PlanRequest) ([]goal.PlanCard, error) {
	node := strings.TrimSpace(p.NodePath)
	if node == "" {
		node = "node"
	}
	runtimePath := strings.TrimSpace(p.RuntimePath)
	if runtimePath == "" {
		return nil, fmt.Errorf("Pi Lead 规划器缺少运行时入口（设置 MAW_PI_RUNTIME 或确认仓库内有 runtimes/pi）")
	}
	if !filepath.IsAbs(runtimePath) {
		return nil, fmt.Errorf("Pi 运行时入口必须是绝对路径，收到 %q", runtimePath)
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg := map[string]any{
		"mode":       "lead",
		"cwd":        filepath.Dir(filepath.Dir(filepath.Dir(runtimePath))),
		"run_id":     "run_lead" + req.GoalID,
		"generation": 1,
		"model":      p.Model,
		"prompt":     leadPlanPrompt(req),
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, node, runtimePath)
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Env = leadEnv()
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("Pi Lead 规划超时（%s）", timeout)
	}

	text, reason, resultOK, parseErr := parsePiResultEvents(stdout.String())
	if parseErr != nil {
		if runErr != nil {
			return nil, fmt.Errorf("Pi Lead 规划失败：%v；stderr: %s", parseErr, truncate(stderr.String(), 400))
		}
		return nil, parseErr
	}
	if !resultOK {
		return nil, fmt.Errorf("Pi Lead 规划未通过（reason=%s）：%s", reason, truncate(text, 400))
	}
	cards, err := goal.ParsePlanCards([]byte(text))
	if err != nil {
		return nil, err
	}
	return cards, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// leadEnv 只传执行必需变量；模型凭据由宿主白名单决定（MAW_PI_* 与供应商键）。
func leadEnv() []string {
	keep := []string{"HOME", "PATH", "LANG", "TMPDIR", "PI_MODEL", "PI_PROVIDER", "PI_BASE_URL", "PI_API_KEY"}
	out := make([]string, 0, len(keep))
	for _, name := range keep {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// leadPlanPrompt 是给 Lead 的固定任务说明：只要计划，不要执行。
func leadPlanPrompt(req goal.PlanRequest) string {
	var b strings.Builder
	b.WriteString("你是 maw 的目标级 Lead。只做规划：不写文件、不改任务状态、不执行命令。\n")
	b.WriteString("把目标拆成可独立领取的任务卡，输出一个 JSON 对象：{\"cards\":[...]}。\n")
	b.WriteString("每张卡必须包含：card_id、id、title、goal（现状→目标）、priority、merge_order、deps、write_scope、read_scope、acceptance、verify、not_doing、origin.source_refs。\n")
	b.WriteString("约束：卡数不超过 ")
	b.WriteString(fmt.Sprint(req.AllowedCards()))
	b.WriteString("；write_scope 用仓库相对路径；同一次修订里写盘范围重叠的卡必须有依赖序；acceptance 与 verify 不许留 TODO。\n\n")
	b.WriteString("目标：" + req.Title + "\n")
	b.WriteString("陈述：" + req.Statement + "\n")
	b.WriteString("目标验收条件：\n")
	for _, a := range req.Acceptance {
		b.WriteString("- " + a.ID + " [" + a.Kind + "] " + a.Check + " — " + a.Statement + "\n")
	}
	if len(req.Evidence) > 0 {
		b.WriteString("可引用的证据：\n")
		for _, e := range req.Evidence {
			b.WriteString("- " + e + "\n")
		}
	}
	return b.String()
}

// parsePiResultEvents 从 NDJSON 事件流里取 result 事件。
func parsePiResultEvents(stream string) (text, reason string, ok bool, err error) {
	found := false
	for _, line := range strings.Split(stream, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Payload struct {
				OK      bool   `json:"ok"`
				Outcome string `json:"outcome"`
				Reason  string `json:"reason"`
				Text    string `json:"text"`
			} `json:"payload"`
		}
		if jsonErr := json.Unmarshal([]byte(line), &event); jsonErr != nil {
			continue
		}
		if event.Type != "result" {
			continue
		}
		found = true
		ok = event.Payload.OK && event.Payload.Outcome == "completed"
		reason = event.Payload.Reason
		text = event.Payload.Text
	}
	if !found {
		return "", "", false, fmt.Errorf("Pi 运行时没有输出 result 事件：无法判定规划结果")
	}
	return text, reason, ok, nil
}

// leadPlannerFactory 是测试注入点：生产按工程路径解析运行时位置。
var leadPlannerFactory = defaultLeadPlanner

func defaultLeadPlanner(pc *projCtx) (LeadPlanner, error) {
	runtimePath := strings.TrimSpace(os.Getenv("MAW_PI_RUNTIME"))
	if runtimePath == "" {
		runtimePath = filepath.Join(pc.Repo, "runtimes", "pi", "src", "main.mjs")
	}
	if _, err := os.Stat(runtimePath); err != nil {
		return nil, fmt.Errorf("找不到 Pi 运行时入口 %q：设置 MAW_PI_RUNTIME 指向 runtimes/pi/src/main.mjs", runtimePath)
	}
	nodePath := strings.TrimSpace(os.Getenv("MAW_PI_NODE"))
	if nodePath == "" {
		resolved, err := exec.LookPath("node")
		if err != nil {
			return nil, fmt.Errorf("未找到 node（Pi Lead 需要 Node >= 22.19）：%v", err)
		}
		nodePath = resolved
	}
	return &PiLeadPlanner{
		NodePath: nodePath, RuntimePath: runtimePath,
		Model: strings.TrimSpace(os.Getenv("PI_MODEL")),
	}, nil
}

// LeadGoalPlanInput 是一次规划请求。
type LeadGoalPlanInput struct {
	GoalID      string
	Title       string
	Statement   string
	IterationID string
	Acceptance  []string
	Evidence    []string
	Kind        string
	MaxCards    int
	PlanRev     int
	DryRun      bool
}

// LeadGoalPlan 生成一份待审计划并落库为 proposed；规划失败不留任何副作用。
//
// 顺序：授权 → 解析验收 → 读目标/修订号（只读）→ 调 Planner（只读）→ 组装校验
// → 同一事务里建目标（如缺）与保存修订。任何一步失败都不会写任务或蓝图。
func LeadGoalPlan(cwd string, in LeadGoalPlanInput) (Result, error) {
	actor, err := leadActor(cwd)
	if err != nil {
		return nil, err
	}
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	acceptance, err := goal.ParseAcceptanceSpec(in.Acceptance)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	statement := strings.TrimSpace(in.Statement)
	if title == "" || statement == "" {
		return nil, fmt.Errorf("需要 --title 与 --statement（现状→目标）")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = goal.PlanKindInitial
	}

	var (
		goalID      = strings.TrimSpace(in.GoalID)
		goalVersion = 1
		planRev     = in.PlanRev
		existing    *goal.Goal
	)
	if err := withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		if goalID == "" {
			return nil
		}
		g, err := db.GetGoal(tx, pc.ID(), goalID)
		if err != nil {
			return err
		}
		existing = g
		if g != nil {
			if goal.IsTerminal(g.Status) {
				return fmt.Errorf("目标 %s 已终结（%s），不能继续规划", g.GoalID, g.Status)
			}
			goalVersion = g.ActiveVersion
		}
		if planRev <= 0 {
			plans, err := db.ListPlanRevisions(tx, pc.ID(), goalID)
			if err != nil {
				return err
			}
			planRev = len(plans) + 1
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if goalID == "" {
		goalID, err = newGoalID()
		if err != nil {
			return nil, err
		}
	}
	if planRev <= 0 {
		planRev = 1
	}

	req := goal.PlanRequest{
		ProjectID: pc.ID(), GoalID: goalID, GoalVersion: goalVersion,
		PlanRev: planRev, Kind: kind, Title: title, Statement: statement,
		Acceptance: acceptance, Evidence: in.Evidence, MaxCards: in.MaxCards,
	}
	planner, err := leadPlannerFactory(pc)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cards, err := planner.Plan(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("规划失败（未产生任何任务副作用）：%w", err)
	}
	rev, err := goal.BuildPlanRevision(req, cards)
	if err != nil {
		return nil, fmt.Errorf("计划校验失败（未产生任何任务副作用）：%w", err)
	}
	summary := goal.SummarizePlan(rev)

	if in.DryRun {
		return Result{
			"ok": true, "dry_run": true, "goal_id": goalID, "plan_rev": planRev,
			"plan_digest": rev.PlanDigest, "summary": summary, "cards": rev.Cards,
			"next": "maw lead goal plan（去掉 --dry-run 落库为 proposed）",
		}, nil
	}

	err = withGlobalWrite(pc.GlobalDB, func(tx *sql.Tx) error {
		if existing == nil {
			g := &goal.Goal{
				ProjectID: pc.ID(), GoalID: goalID, IterationID: in.IterationID,
				Status: goal.StatusDraft, ActiveVersion: 1,
			}
			v := &goal.Version{
				ProjectID: pc.ID(), GoalID: goalID, GoalVersion: 1,
				Title: title, Statement: statement, Acceptance: acceptance,
				Budget: goal.DefaultBudget(),
			}
			if _, _, err := db.CreateGoal(tx, g, v, actor); err != nil {
				return err
			}
		}
		if _, err := db.SavePlanRevision(tx, rev, actor); err != nil {
			return err
		}
		// Lead 的模型调用独立归属（契约 §10 第 1 条）：把 goal/plan_rev 写进审计事件，
		// 统计侧按 usage_kind=lead 归集，不并进 Worker 的 Run 用量。
		return db.AppendEvent(tx, pc.ID(), actor, "goal_lead_call", "", map[string]any{
			"usage_kind": "lead", "goal_id": goalID, "plan_rev": planRev,
			"plan_digest": rev.PlanDigest, "cards": len(rev.Cards),
			"model": strings.TrimSpace(os.Getenv("PI_MODEL")),
		})
	})
	if err != nil {
		return nil, err
	}
	return Result{
		"ok": true, "goal_id": goalID, "plan_rev": planRev, "status": rev.Status,
		"plan_digest": rev.PlanDigest, "summary": summary, "cards": rev.Cards,
		"next": fmt.Sprintf("maw lead goal approve --goal %s --rev %d", goalID, planRev),
	}, nil
}

// newGoalID 生成 gl_ + 12 位小写十六进制（契约 §2 形态）。
func newGoalID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "gl_" + hex.EncodeToString(buf), nil
}

// LeadGoalApproveInput 是批准请求。
type LeadGoalApproveInput struct {
	GoalID      string
	PlanRev     int
	Policy      string
	AutoEnqueue bool
}

// LeadGoalApprove 记录一次审批；默认停在人工审查，只有显式 auto_enqueue 才入队。
func LeadGoalApprove(cwd string, in LeadGoalApproveInput) (Result, error) {
	actor, err := leadActor(cwd)
	if err != nil {
		return nil, err
	}
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.GoalID) == "" || in.PlanRev < 1 {
		return nil, fmt.Errorf("需要 --goal 与 --rev")
	}
	policy := strings.TrimSpace(in.Policy)
	if policy == "" {
		policy = goal.PolicyManual
	}
	if !goal.ValidPolicySource(policy) {
		return nil, fmt.Errorf("策略只能是 manual 或 auto_enqueue，收到 %q", policy)
	}
	if in.AutoEnqueue && policy != goal.PolicyAutoEnqueue {
		return nil, fmt.Errorf("自动入队需要显式策略 auto_enqueue（--policy auto_enqueue），当前 %q", policy)
	}

	var approved *goal.PlanRevision
	var changed bool
	if err := withGlobalWrite(pc.GlobalDB, func(tx *sql.Tx) error {
		rev, err := db.GetPlanRevision(tx, pc.ID(), in.GoalID, in.PlanRev)
		if err != nil {
			return err
		}
		if rev == nil {
			return fmt.Errorf("%w: %s rev %d", goal.ErrPlanNotFound, in.GoalID, in.PlanRev)
		}
		approved, changed, err = db.ApprovePlanRevision(
			tx, pc.ID(), in.GoalID, in.PlanRev, actor, policy, rev.PlanDigest, actor)
		return err
	}); err != nil {
		return nil, err
	}

	out := Result{
		"ok": true, "goal_id": in.GoalID, "plan_rev": in.PlanRev,
		"approved": changed, "approver": actor, "policy": policy,
		"plan_digest": approved.PlanDigest, "status": approved.Status,
	}
	if !in.AutoEnqueue {
		out["next"] = fmt.Sprintf("maw lead goal enqueue --goal %s --rev %d", in.GoalID, in.PlanRev)
		return out, nil
	}
	enq, err := PiAppendPlan(PiAppendInput{
		Cwd: cwd, Plan: *approved, Actor: actor,
	})
	if err != nil {
		return nil, err
	}
	out["enqueue"] = enq
	out["next"] = "maw lead goal show --goal " + in.GoalID
	return out, nil
}

// LeadGoalEnqueueInput 是入队请求。
type LeadGoalEnqueueInput struct {
	GoalID  string
	PlanRev int
	DryRun  bool
}

// LeadGoalEnqueue 把已批准的计划交给 PI-302 的追加入队入口。
func LeadGoalEnqueue(cwd string, in LeadGoalEnqueueInput) (Result, error) {
	actor, err := leadActor(cwd)
	if err != nil {
		return nil, err
	}
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.GoalID) == "" || in.PlanRev < 1 {
		return nil, fmt.Errorf("需要 --goal 与 --rev")
	}
	var rev *goal.PlanRevision
	if err := withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		rev, e = db.GetPlanRevision(tx, pc.ID(), in.GoalID, in.PlanRev)
		if e != nil {
			return e
		}
		if rev == nil {
			return fmt.Errorf("%w: %s rev %d", goal.ErrPlanNotFound, in.GoalID, in.PlanRev)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if rev.Status != goal.PlanApproved && rev.Status != goal.PlanApplied {
		return nil, fmt.Errorf("计划修订 %d 当前 %s：先 maw lead goal approve", rev.PlanRev, rev.Status)
	}
	return PiAppendPlan(PiAppendInput{Cwd: cwd, Plan: *rev, Actor: actor, DryRun: in.DryRun})
}

// LeadGoalShow 只读展示目标、修订、映射与所关联任务。
func LeadGoalShow(cwd, goalID string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return nil, fmt.Errorf("需要 --goal")
	}
	var snap *db.GoalSnapshot
	if err := withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		snap, e = db.LoadGoalSnapshot(tx, pc.ID(), goalID)
		return e
	}); err != nil {
		return nil, err
	}
	plans := make([]Result, 0, len(snap.Plans))
	for _, rev := range snap.Plans {
		plans = append(plans, Result{
			"plan_rev": rev.PlanRev, "kind": rev.Kind, "status": rev.Status,
			"plan_digest": rev.PlanDigest, "cards": len(rev.Cards),
			"approved_by": rev.Approval.Approver, "applied_at": rev.AppliedAt,
		})
	}
	links := make([]Result, 0, len(snap.TaskLinks))
	for _, l := range snap.TaskLinks {
		links = append(links, Result{
			"card_id": l.CardID, "display_id": l.DisplayID, "instance_id": l.InstanceID,
			"version_no": l.VersionNo, "state": l.State,
		})
	}
	return Result{
		"ok": true, "goal": snap.Goal, "active_version": snap.Active,
		"plans": plans, "links": links, "tasks": snap.Tasks,
		"enqueues": len(snap.Enqueues),
		"next":     "maw lead goal plan / approve / enqueue",
	}, nil
}

// LeadGoalReworkInput 是一次返工规划请求。
type LeadGoalReworkInput struct {
	GoalID string
	DryRun bool
}

// LeadGoalRework 按整体验收的失败断言生成返工计划修订（契约 §12）。
//
// 三条约束：
//   - 只有真的失败（且不是 unknown）才返工；
//   - 每张返工卡必须带失败证据（G216），证据来自本次验收；
//   - 返工受 max_rework_rounds / max_plan_revisions 限制，用尽就升级人工。
func LeadGoalRework(cwd string, in LeadGoalReworkInput) (Result, error) {
	actor, err := leadActor(cwd)
	if err != nil {
		return nil, err
	}
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	goalID := strings.TrimSpace(in.GoalID)
	if goalID == "" {
		return nil, fmt.Errorf("需要 --goal")
	}
	acceptRes, err := LeadGoalAccept(cwd, LeadGoalAcceptInput{GoalID: goalID, DryRun: true})
	if err != nil {
		return nil, err
	}
	proposal, _ := acceptRes["rework"].(goal.ReworkProposal)
	overall, _ := acceptRes["overall"].(string)
	if overall != goal.VerdictFail {
		return nil, fmt.Errorf("目标验收结论是 %s，不是 fail：只有失败才需要返工（unknown 请先补证据）", overall)
	}
	if len(proposal.Targets) == 0 {
		return nil, fmt.Errorf("没有失败断言，拒绝生成空返工")
	}

	var snap *db.GoalSnapshot
	if err := withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		snap, e = db.LoadGoalSnapshot(tx, pc.ID(), goalID)
		return e
	}); err != nil {
		return nil, err
	}
	budget := goalReworkBudget(snap)
	if !budget.ReworkAllowed() {
		return nil, fmt.Errorf("返工预算已用尽（返工 %d/%d 轮，修订 %d/%d）：目标应升级人工，不再自动加卡",
			budget.UsedReworkRounds, budget.MaxReworkRounds, budget.UsedPlanRevisions, budget.MaxPlanRevisions)
	}
	if snap == nil || snap.Active == nil {
		return nil, fmt.Errorf("目标 %s 缺少当前版本定义", goalID)
	}

	planRev := len(snap.Plans) + 1
	req := goal.PlanRequest{
		ProjectID: pc.ID(), GoalID: goalID, GoalVersion: snap.Goal.ActiveVersion,
		PlanRev: planRev, Kind: goal.PlanKindRework,
		Title: snap.Active.Title, Statement: snap.Active.Statement,
		Acceptance: snap.Active.Acceptance, Evidence: proposal.Evidence,
		MaxCards: snap.Active.Budget.MaxCardsPerRevision,
	}
	planner, err := leadPlannerFactory(pc)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cards, err := planner.Plan(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("返工规划失败（未产生任何任务副作用）：%w", err)
	}
	for i := range cards {
		if len(cards[i].Origin.Evidence) == 0 {
			cards[i].Origin.Evidence = append([]string{}, proposal.Evidence...)
		}
	}
	rev, err := goal.BuildPlanRevision(req, cards)
	if err != nil {
		return nil, fmt.Errorf("返工计划校验失败（未产生任何任务副作用）：%w", err)
	}

	// 同一失败证据集只落一份修订：卡片内容相同就返回已有修订。
	// 比较时把 plan_rev 归一为 0——修订号每次都递增，带着它比会把同一份计划判成不同的。
	var existing *goal.PlanRevision
	want := normalizedPlanDigest(rev)
	for _, p := range snap.Plans {
		if p.Kind == goal.PlanKindRework && strings.EqualFold(normalizedPlanDigest(p), want) {
			existing = p
			break
		}
	}
	if existing != nil {
		return Result{
			"ok": true, "replayed": true, "goal_id": goalID, "plan_rev": existing.PlanRev,
			"status": existing.Status, "plan_digest": existing.PlanDigest,
			"rework": proposal, "next": "同一失败证据已有返工修订，不重复创建",
		}, nil
	}

	if in.DryRun {
		return Result{
			"ok": true, "dry_run": true, "goal_id": goalID, "plan_rev": planRev,
			"plan_digest": rev.PlanDigest, "rework": proposal, "cards": rev.Cards,
			"next": "去掉 --dry-run 落库为返工修订",
		}, nil
	}
	if err := withGlobalWrite(pc.GlobalDB, func(tx *sql.Tx) error {
		if _, err := db.SavePlanRevision(tx, rev, actor); err != nil {
			return err
		}
		return db.AppendEvent(tx, pc.ID(), actor, "goal_rework_proposed", "", map[string]any{
			"goal_id": goalID, "plan_rev": planRev, "plan_digest": rev.PlanDigest,
			"rework_key": proposal.Key, "targets": proposal.Targets,
			"evidence": proposal.Evidence, "usage_kind": "lead",
		})
	}); err != nil {
		return nil, err
	}
	return Result{
		"ok": true, "goal_id": goalID, "plan_rev": planRev, "status": rev.Status,
		"plan_digest": rev.PlanDigest, "rework": proposal,
		"next": fmt.Sprintf("maw lead goal approve --goal %s --rev %d", goalID, planRev),
	}, nil
}

// normalizedPlanDigest 忽略修订号后的计划摘要，用于返工重放去重。
func normalizedPlanDigest(r *goal.PlanRevision) string {
	if r == nil {
		return ""
	}
	copyRev := *r
	copyRev.PlanRev = 0
	digest, err := goal.Digest(&copyRev)
	if err != nil {
		return ""
	}
	return digest
}
