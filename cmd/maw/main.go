package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/wuhao/maw/internal/app"
	"github.com/wuhao/maw/internal/errx"
	"github.com/wuhao/maw/internal/version"
)

func mustFlag(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func main() {
	var asJSON, dryRun bool
	var cwd string

	root := &cobra.Command{
		Use:     "maw",
		Short:   "Multi-agent worktree orchestrator",
		Version: version.Version,
	}
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "JSON output")
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Preview without writing")
	root.PersistentFlags().StringVarP(&cwd, "cwd", "C", "", "Working directory")

	printRes := func(r app.Result, err error) {
		if err != nil {
			code := 1
			if e, ok := err.(*errx.Error); ok {
				code = e.Code
				if asJSON {
					b, _ := json.MarshalIndent(map[string]any{"ok": false, "error": e.Msg, "next": e.Next}, "", "  ")
					fmt.Println(string(b))
				} else {
					fmt.Fprintln(os.Stderr, e.Error())
				}
			} else {
				if asJSON {
					b, _ := json.MarshalIndent(map[string]any{"ok": false, "error": err.Error()}, "", "  ")
					fmt.Println(string(b))
				} else {
					fmt.Fprintln(os.Stderr, "Error:", err)
				}
			}
			os.Exit(code)
		}
		if asJSON {
			b, _ := json.MarshalIndent(r, "", "  ")
			fmt.Println(string(b))
			if ok, _ := r["ok"].(bool); !ok && r["ok"] != nil {
				os.Exit(1)
			}
			return
		}
		if term, ok := r["terminal"].(string); ok {
			fmt.Print(term)
			if h, ok := r["html"].(string); ok {
				fmt.Println("\nHTML 文件:", h)
			}
			if u, ok := r["url"].(string); ok {
				fmt.Println("实时地址:", u)
			}
			if n, ok := r["next"].(string); ok {
				fmt.Println("下一步:", n)
			}
			return
		}
		if ok, _ := r["ok"].(bool); ok {
			fmt.Println("OK")
		}
		for _, k := range []string{"home", "global_db", "project_id", "project_name", "worktree", "branch", "task_id", "status", "board", "html", "agent", "registered", "role", "url", "protocol_fallback", "worktrees", "branches", "removed_worktrees", "removed_branches"} {
			if v, ok := r[k]; ok {
				fmt.Printf("%s: %v\n", k, v)
			}
		}
		if det, ok := r["protocol_detected"].(map[string]string); ok && len(det) > 0 {
			fmt.Println("protocol_detected:")
			for id, reason := range det {
				fmt.Printf("  - %s: %s\n", id, reason)
			}
		}
		if agents, ok := r["agents"]; ok {
			b, _ := json.MarshalIndent(agents, "", "  ")
			fmt.Println("agents:", string(b))
		}
		if t, ok := r["task"]; ok {
			b, _ := json.Marshal(t)
			fmt.Println("task:", string(b))
		}
		if res, ok := r["results"]; ok {
			b, _ := json.Marshal(res)
			fmt.Println("results:", string(b))
		}
		if issues, ok := r["issues"].([]string); ok && len(issues) > 0 {
			fmt.Println("Doctor: FAIL")
			for _, i := range issues {
				fmt.Println("  -", i)
			}
		}
		if adv, ok := r["branch_protection_advice"].([]string); ok {
			fmt.Println("Branch protection advice:")
			for _, a := range adv {
				fmt.Println("  -", a)
			}
		}
		if n, ok := r["next"].(string); ok {
			fmt.Println("Next:", n)
		}
		if ok, exists := r["ok"].(bool); exists && !ok {
			os.Exit(1)
		}
	}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize .multiagent environment",
		Run: func(cmd *cobra.Command, args []string) {
			upgrade, _ := cmd.Flags().GetBool("upgrade")
			printRes(app.Init(cwd, dryRun, upgrade))
		},
	}
	initCmd.Flags().Bool("upgrade", false, "upgrade protocol/config")
	root.AddCommand(initCmd)

	planCmd := &cobra.Command{Use: "plan", Short: "Blueprint commands"}
	planCmd.AddCommand(&cobra.Command{
		Use:  "apply [blueprint]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			yes, _ := cmd.Flags().GetBool("yes")
			reset, _ := cmd.Flags().GetBool("reset")
			printRes(app.PlanApply(args[0], cwd, yes, dryRun, reset))
		},
	})
	planCmd.Commands()[0].Flags().Bool("yes", false, "confirm apply")
	planCmd.Commands()[0].Flags().Bool("reset", false, "清空该工程全部任务后再灌入（默认增量合并，保留 done/进行中）")
	planCmd.AddCommand(&cobra.Command{
		Use:  "check [blueprint]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			printRes(app.PlanCheck(args[0], cwd))
		},
	})
	root.AddCommand(planCmd)

	regCmd := &cobra.Command{
		Use:   "register",
		Short: "向 maw 注册当前 Agent（领取前后均可）",
		Run: func(cmd *cobra.Command, args []string) {
			agent, _ := cmd.Flags().GetString("agent")
			role, _ := cmd.Flags().GetString("role")
			printRes(app.Register(cwd, agent, role))
		},
	}
	regCmd.Flags().String("agent", "", "Agent 唯一名（默认读 MAW_AGENT；未指定则自动分配 {应用}-{工程}-{序号}）")
	regCmd.Flags().String("role", "worker", "角色：worker|reviewer|integrator")
	root.AddCommand(regCmd)

	unregCmd := &cobra.Command{
		Use:   "unregister",
		Short: "注销当前 Agent",
		Run: func(cmd *cobra.Command, args []string) {
			agent, _ := cmd.Flags().GetString("agent")
			printRes(app.Unregister(cwd, agent))
		},
	}
	unregCmd.Flags().String("agent", "", "Agent 唯一名")
	root.AddCommand(unregCmd)

	root.AddCommand(&cobra.Command{
		Use:   "agents",
		Short: "列出已注册 Agent 及其当前任务",
		Run:   func(cmd *cobra.Command, args []string) { printRes(app.AgentsList(cwd)) },
	})

	nextCmd := &cobra.Command{
		Use:   "next",
		Short: "领取任务并自动注册执行者",
		Run: func(cmd *cobra.Command, args []string) {
			agent, _ := cmd.Flags().GetString("agent")
			printRes(app.Next(cwd, agent, dryRun))
		},
	}
	nextCmd.Flags().String("agent", "", "Agent 唯一名（默认读 MAW_AGENT；未指定则自动分配 {应用}-{工程}-{序号}）")
	root.AddCommand(nextCmd)

	progCmd := &cobra.Command{
		Use: "progress",
		Run: func(cmd *cobra.Command, args []string) {
			msg, _ := cmd.Flags().GetString("message")
			task, _ := cmd.Flags().GetString("task")
			printRes(app.Progress(cwd, msg, task))
		},
	}
	progCmd.Flags().StringP("message", "m", "", "milestone message")
	_ = progCmd.MarkFlagRequired("message")
	progCmd.Flags().String("task", "", "task id")
	root.AddCommand(progCmd)

	subCmd := &cobra.Command{
		Use: "submit",
		Run: func(cmd *cobra.Command, args []string) {
			task, _ := cmd.Flags().GetString("task")
			printRes(app.Submit(cwd, task, dryRun))
		},
	}
	subCmd.Flags().String("task", "", "task id")
	root.AddCommand(subCmd)

	revCmd := &cobra.Command{
		Use:  "review [task_id]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			approve, _ := cmd.Flags().GetBool("approve")
			req, _ := cmd.Flags().GetBool("request-changes")
			msg, _ := cmd.Flags().GetString("message")
			if !approve && !req {
				printRes(nil, errx.New("specify --approve or --request-changes", ""))
				return
			}
			printRes(app.Review(cwd, args[0], approve, msg))
		},
	}
	revCmd.Flags().Bool("approve", false, "approve")
	revCmd.Flags().Bool("request-changes", false, "request changes")
	revCmd.Flags().StringP("message", "m", "", "review message")
	root.AddCommand(revCmd)

	mergeCmd := &cobra.Command{
		Use: "merge",
		Run: func(cmd *cobra.Command, args []string) {
			yes, _ := cmd.Flags().GetBool("yes")
			printRes(app.Merge(cwd, yes, dryRun))
		},
	}
	mergeCmd.Flags().Bool("yes", false, "confirm merge")
	root.AddCommand(mergeCmd)

	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "清理残留 worktree 与无主 agent/* 分支（默认预览，--yes 才删除）",
		Run: func(cmd *cobra.Command, args []string) {
			yes, _ := cmd.Flags().GetBool("yes")
			printRes(app.Prune(cwd, yes, dryRun))
		},
	}
	pruneCmd.Flags().Bool("yes", false, "确认删除（默认仅列出）")
	root.AddCommand(pruneCmd)

	boardCmd := &cobra.Command{
		Use:   "board",
		Short: "全局中文任务看板（跨工程；终端 + 动态 HTML）",
		Run: func(cmd *cobra.Command, args []string) {
			serve, _ := cmd.Flags().GetBool("serve")
			addr, _ := cmd.Flags().GetString("addr")
			printRes(app.Board(cwd, serve, addr, app.BoardFilters{
				Project: mustFlag(cmd, "project"),
				Agent:   mustFlag(cmd, "agent"),
				Status:  mustFlag(cmd, "status"),
			}))
		},
	}
	boardCmd.Flags().Bool("serve", false, "启动本地 HTTP 服务，看板每 3 秒自动刷新")
	boardCmd.Flags().String("addr", "127.0.0.1:7420", "看板服务监听地址")
	boardCmd.Flags().String("project", "", "按工程 ID 筛选（默认全部）")
	boardCmd.Flags().String("agent", "", "按 Agent 名筛选（默认全部）")
	boardCmd.Flags().String("status", "", "按任务状态筛选（默认全部）")
	root.AddCommand(boardCmd)
	root.AddCommand(&cobra.Command{Use: "doctor", Run: func(cmd *cobra.Command, args []string) { printRes(app.Doctor(cwd)) }})

	blockCmd := &cobra.Command{
		Use: "block",
		Run: func(cmd *cobra.Command, args []string) {
			msg, _ := cmd.Flags().GetString("message")
			task, _ := cmd.Flags().GetString("task")
			printRes(app.Block(cwd, msg, task))
		},
	}
	blockCmd.Flags().StringP("message", "m", "", "reason")
	_ = blockCmd.MarkFlagRequired("message")
	blockCmd.Flags().String("task", "", "task id")
	root.AddCommand(blockCmd)

	taskCmd := &cobra.Command{Use: "task"}
	taskCmd.AddCommand(&cobra.Command{Use: "list", Run: func(cmd *cobra.Command, args []string) { printRes(app.TaskList(cwd)) }})
	taskCmd.AddCommand(&cobra.Command{
		Use:  "show [id]",
		Args: cobra.ExactArgs(1),
		Run:  func(cmd *cobra.Command, args []string) { printRes(app.TaskShow(cwd, args[0])) },
	})
	taskCmd.AddCommand(&cobra.Command{
		Use:  "unblock [id]",
		Args: cobra.ExactArgs(1),
		Run:  func(cmd *cobra.Command, args []string) { printRes(app.Unblock(cwd, args[0])) },
	})
	root.AddCommand(taskCmd)

	hookCmd := &cobra.Command{Use: "hook", Hidden: true}
	hookCmd.AddCommand(&cobra.Command{
		Use: "pre-commit",
		Run: func(cmd *cobra.Command, args []string) { printRes(app.HookPreCommit(cwd)) },
	})
	hookCmd.AddCommand(&cobra.Command{
		Use: "pre-push",
		Run: func(cmd *cobra.Command, args []string) { printRes(app.HookPrePush(cwd, os.Stdin)) },
	})
	root.AddCommand(hookCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
