package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/wuhao/goal-agents/internal/app"
	"github.com/wuhao/goal-agents/internal/errx"
	"github.com/wuhao/goal-agents/internal/version"
)

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
				fmt.Println("\nHTML:", h)
			}
			if n, ok := r["next"].(string); ok {
				fmt.Println("Next:", n)
			}
			return
		}
		if ok, _ := r["ok"].(bool); ok {
			fmt.Println("OK")
		}
		for _, k := range []string{"home", "worktree", "branch", "task_id", "status", "board", "html"} {
			if v, ok := r[k]; ok {
				fmt.Printf("%s: %v\n", k, v)
			}
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
			printRes(app.PlanApply(args[0], cwd, yes, dryRun))
		},
	})
	planCmd.Commands()[0].Flags().Bool("yes", false, "confirm apply")
	planCmd.AddCommand(&cobra.Command{
		Use:  "check [blueprint]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			printRes(app.PlanCheck(args[0], cwd))
		},
	})
	root.AddCommand(planCmd)

	nextCmd := &cobra.Command{
		Use:   "next",
		Short: "Claim next ready task + worktree",
		Run: func(cmd *cobra.Command, args []string) {
			agent, _ := cmd.Flags().GetString("agent")
			printRes(app.Next(cwd, agent, dryRun))
		},
	}
	nextCmd.Flags().String("agent", "", "override MAW_AGENT")
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

	root.AddCommand(&cobra.Command{Use: "board", Run: func(cmd *cobra.Command, args []string) { printRes(app.Board(cwd)) }})
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
