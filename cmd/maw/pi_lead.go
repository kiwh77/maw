package main

import (
	"github.com/spf13/cobra"

	"github.com/kiwh77/maw/internal/app"
)

// addLeadGoalCommands 注册 `maw lead goal ...`（契约 maw.lead.goal/v1 §5–§8）。
//
// 四个动作与状态严格对应，不提供「一步到位」的隐式入队：
//
//	plan     规划 → 落 proposed（可 --dry-run 只看不写）
//	approve  记录审批（默认 manual；--policy auto_enqueue --auto-enqueue 才入队）
//	enqueue  把已批准修订交给追加入队
//	show     只读回看目标、修订、映射与关联任务
func addLeadGoalCommands(leadCmd *cobra.Command, cwd *string, printRes func(app.Result, error)) {
	goalCmd := &cobra.Command{
		Use:   "goal",
		Short: "目标级 Lead：规划目标、审查计划、批准入队",
	}

	planCmd := &cobra.Command{
		Use:   "plan",
		Short: "由 Pi Lead 生成结构化计划并落为待审修订（默认不写蓝图）",
		Run: func(cmd *cobra.Command, args []string) {
			goalID, _ := cmd.Flags().GetString("goal")
			title, _ := cmd.Flags().GetString("title")
			statement, _ := cmd.Flags().GetString("statement")
			iteration, _ := cmd.Flags().GetString("iteration")
			kind, _ := cmd.Flags().GetString("kind")
			acceptance, _ := cmd.Flags().GetStringSlice("acceptance")
			evidence, _ := cmd.Flags().GetStringSlice("evidence")
			maxCards, _ := cmd.Flags().GetInt("max-cards")
			rev, _ := cmd.Flags().GetInt("rev")
			dry, _ := cmd.Flags().GetBool("dry-run")
			printRes(app.LeadGoalPlan(*cwd, app.LeadGoalPlanInput{
				GoalID: goalID, Title: title, Statement: statement,
				IterationID: iteration, Kind: kind, Acceptance: acceptance,
				Evidence: evidence, MaxCards: maxCards, PlanRev: rev, DryRun: dry,
			}))
		},
	}
	planCmd.Flags().String("goal", "", "目标 id（留空新建）")
	planCmd.Flags().String("title", "", "目标标题")
	planCmd.Flags().String("statement", "", "目标陈述（现状→目标）")
	planCmd.Flags().String("iteration", "", "归属迭代 id（可空）")
	planCmd.Flags().String("kind", "initial", "计划类型：initial / rework")
	planCmd.Flags().StringSlice("acceptance", nil, "目标验收条件，可重复：command:检查|说明")
	planCmd.Flags().StringSlice("evidence", nil, "可引用证据（文档章节、issue 等）")
	planCmd.Flags().Int("max-cards", 0, "本次修订卡数上限（0 用预算缺省）")
	planCmd.Flags().Int("rev", 0, "修订号（0 自动取下一号）")

	approveCmd := &cobra.Command{
		Use:   "approve",
		Short: "批准一份计划修订；只有显式 auto_enqueue 策略才顺带入队",
		Run: func(cmd *cobra.Command, args []string) {
			goalID, _ := cmd.Flags().GetString("goal")
			rev, _ := cmd.Flags().GetInt("rev")
			policy, _ := cmd.Flags().GetString("policy")
			auto, _ := cmd.Flags().GetBool("auto-enqueue")
			printRes(app.LeadGoalApprove(*cwd, app.LeadGoalApproveInput{
				GoalID: goalID, PlanRev: rev, Policy: policy, AutoEnqueue: auto,
			}))
		},
	}
	approveCmd.Flags().String("goal", "", "目标 id")
	approveCmd.Flags().Int("rev", 0, "计划修订号")
	approveCmd.Flags().String("policy", "manual", "审批策略：manual / auto_enqueue")
	approveCmd.Flags().Bool("auto-enqueue", false, "批准后立即入队（需 --policy auto_enqueue）")

	enqueueCmd := &cobra.Command{
		Use:   "enqueue",
		Short: "把已批准的计划修订增量入队（幂等，可重放）",
		Run: func(cmd *cobra.Command, args []string) {
			goalID, _ := cmd.Flags().GetString("goal")
			rev, _ := cmd.Flags().GetInt("rev")
			dry, _ := cmd.Flags().GetBool("dry-run")
			printRes(app.LeadGoalEnqueue(*cwd, app.LeadGoalEnqueueInput{
				GoalID: goalID, PlanRev: rev, DryRun: dry,
			}))
		},
	}
	enqueueCmd.Flags().String("goal", "", "目标 id")
	enqueueCmd.Flags().Int("rev", 0, "计划修订号")

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "只读回看目标、修订、映射与关联任务",
		Run: func(cmd *cobra.Command, args []string) {
			goalID, _ := cmd.Flags().GetString("goal")
			if goalID == "" && len(args) > 0 {
				goalID = args[0]
			}
			printRes(app.LeadGoalShow(*cwd, goalID))
		},
	}
	showCmd.Flags().String("goal", "", "目标 id")

	goalCmd.AddCommand(planCmd, approveCmd, enqueueCmd, showCmd)
	leadCmd.AddCommand(goalCmd)
}
