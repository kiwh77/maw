package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/version"
)

func mustFlag(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func main() {
	_ = i18n.Resolve()
	var asJSON, dryRun bool
	var cwd string

	root := &cobra.Command{
		Use:     "maw",
		Short:   i18n.T("cmd.root.short"),
		Version: version.Version,
	}
	root.PersistentFlags().BoolVar(&asJSON, "json", false, i18n.T("flag.json"))
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, i18n.T("flag.dry_run"))
	root.PersistentFlags().StringVarP(&cwd, "cwd", "C", "", i18n.T("flag.cwd"))

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
				fmt.Println("\n"+i18n.T("out.html_file")+":", h)
			}
			if u, ok := r["url"].(string); ok {
				fmt.Println(i18n.T("out.live_url")+":", u)
			}
			if n, ok := r["next"].(string); ok {
				fmt.Println(i18n.T("out.next")+":", n)
			}
			return
		}
		if ok, _ := r["ok"].(bool); ok {
			fmt.Println(i18n.T("out.ok"))
		}
		for _, k := range []string{"home", "global_db", "project_id", "project_name", "worktree", "branch", "task_id", "status", "board", "html", "agent", "registered", "role", "url", "log_path", "protocol_fallback", "worktrees", "branches", "removed_worktrees", "removed_branches"} {
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
			fmt.Println(i18n.T("out.doctor_fail"))
			for _, i := range issues {
				fmt.Println("  -", i)
			}
		}
		if adv, ok := r["branch_protection_advice"].([]string); ok {
			fmt.Println(i18n.T("out.branch_advice"))
			for _, a := range adv {
				fmt.Println("  -", a)
			}
		}
		if n, ok := r["next"].(string); ok {
			fmt.Println(i18n.T("out.next")+":", n)
		}
		if ok, exists := r["ok"].(bool); exists && !ok {
			os.Exit(1)
		}
	}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: i18n.T("cmd.init.short"),
		Run: func(cmd *cobra.Command, args []string) {
			upgrade, _ := cmd.Flags().GetBool("upgrade")
			printRes(app.Init(cwd, dryRun, upgrade))
		},
	}
	initCmd.Flags().Bool("upgrade", false, i18n.T("flag.upgrade"))
	root.AddCommand(initCmd)

	planCmd := &cobra.Command{Use: "plan", Short: i18n.T("cmd.plan.short")}
	planCmd.AddCommand(&cobra.Command{
		Use:  "apply [blueprint]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			yes, _ := cmd.Flags().GetBool("yes")
			reset, _ := cmd.Flags().GetBool("reset")
			printRes(app.PlanApply(args[0], cwd, yes, dryRun, reset))
		},
	})
	planCmd.Commands()[0].Flags().Bool("yes", false, i18n.T("flag.yes"))
	planCmd.Commands()[0].Flags().Bool("reset", false, i18n.T("flag.reset"))
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
		Short: i18n.T("cmd.register.short"),
		Run: func(cmd *cobra.Command, args []string) {
			agent, _ := cmd.Flags().GetString("agent")
			role, _ := cmd.Flags().GetString("role")
			printRes(app.Register(cwd, agent, role))
		},
	}
	regCmd.Flags().String("agent", "", i18n.T("flag.agent"))
	regCmd.Flags().String("role", "worker", i18n.T("flag.role"))
	root.AddCommand(regCmd)

	unregCmd := &cobra.Command{
		Use:   "unregister",
		Short: i18n.T("cmd.unregister.short"),
		Run: func(cmd *cobra.Command, args []string) {
			agent, _ := cmd.Flags().GetString("agent")
			printRes(app.Unregister(cwd, agent))
		},
	}
	unregCmd.Flags().String("agent", "", i18n.T("flag.agent"))
	root.AddCommand(unregCmd)

	root.AddCommand(&cobra.Command{
		Use:   "agents",
		Short: i18n.T("cmd.agents.short"),
		Run:   func(cmd *cobra.Command, args []string) { printRes(app.AgentsList(cwd)) },
	})

	nextCmd := &cobra.Command{
		Use:   "next",
		Short: i18n.T("cmd.next.short"),
		Run: func(cmd *cobra.Command, args []string) {
			agent, _ := cmd.Flags().GetString("agent")
			logPath, _ := cmd.Flags().GetString("log-path")
			printRes(app.Next(cwd, agent, dryRun, logPath))
		},
	}
	nextCmd.Flags().String("agent", "", i18n.T("flag.agent"))
	nextCmd.Flags().String("log-path", "", i18n.T("flag.log_path"))
	root.AddCommand(nextCmd)

	progCmd := &cobra.Command{
		Use: "progress",
		Run: func(cmd *cobra.Command, args []string) {
			msg, _ := cmd.Flags().GetString("message")
			task, _ := cmd.Flags().GetString("task")
			printRes(app.Progress(cwd, msg, task))
		},
	}
	progCmd.Flags().StringP("message", "m", "", i18n.T("flag.message"))
	_ = progCmd.MarkFlagRequired("message")
	progCmd.Flags().String("task", "", i18n.T("flag.task"))
	root.AddCommand(progCmd)

	subCmd := &cobra.Command{
		Use: "submit",
		Run: func(cmd *cobra.Command, args []string) {
			task, _ := cmd.Flags().GetString("task")
			printRes(app.Submit(cwd, task, dryRun))
		},
	}
	subCmd.Flags().String("task", "", i18n.T("flag.task"))
	root.AddCommand(subCmd)

	revCmd := &cobra.Command{
		Use:  "review [task_id]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			approve, _ := cmd.Flags().GetBool("approve")
			req, _ := cmd.Flags().GetBool("request-changes")
			msg, _ := cmd.Flags().GetString("message")
			if !approve && !req {
				printRes(nil, i18n.E("err.review_flags", ""))
				return
			}
			printRes(app.Review(cwd, args[0], approve, msg))
		},
	}
	revCmd.Flags().Bool("approve", false, i18n.T("flag.approve"))
	revCmd.Flags().Bool("request-changes", false, i18n.T("flag.request_changes"))
	revCmd.Flags().StringP("message", "m", "", i18n.T("flag.message"))
	root.AddCommand(revCmd)

	mergeCmd := &cobra.Command{
		Use: "merge",
		Run: func(cmd *cobra.Command, args []string) {
			yes, _ := cmd.Flags().GetBool("yes")
			printRes(app.Merge(cwd, yes, dryRun))
		},
	}
	mergeCmd.Flags().Bool("yes", false, i18n.T("flag.yes"))
	root.AddCommand(mergeCmd)

	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: i18n.T("cmd.prune.short"),
		Run: func(cmd *cobra.Command, args []string) {
			yes, _ := cmd.Flags().GetBool("yes")
			printRes(app.Prune(cwd, yes, dryRun))
		},
	}
	pruneCmd.Flags().Bool("yes", false, i18n.T("flag.yes_delete"))
	root.AddCommand(pruneCmd)

	boardFilters := func(cmd *cobra.Command) app.BoardFilters {
		return app.BoardFilters{
			Project: mustFlag(cmd, "project"),
			Agent:   mustFlag(cmd, "agent"),
			Status:  mustFlag(cmd, "status"),
		}
	}
	boardCmd := &cobra.Command{
		Use:   "board",
		Short: i18n.T("cmd.board.short"),
		Run: func(cmd *cobra.Command, args []string) {
			serve, _ := cmd.Flags().GetBool("serve")
			addr, _ := cmd.Flags().GetString("addr")
			printRes(app.Board(cwd, serve, addr, boardFilters(cmd)))
		},
	}
	boardCmd.Flags().Bool("serve", false, i18n.T("flag.serve"))
	boardCmd.Flags().String("addr", "127.0.0.1:7420", i18n.T("flag.addr"))
	boardCmd.Flags().String("project", "", i18n.T("flag.project"))
	boardCmd.Flags().String("agent", "", i18n.T("flag.agent_filter"))
	boardCmd.Flags().String("status", "", i18n.T("flag.status_filter"))
	root.AddCommand(boardCmd)

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: i18n.T("cmd.serve.short"),
		Run: func(cmd *cobra.Command, args []string) {
			addr, _ := cmd.Flags().GetString("addr")
			printRes(app.Serve(cwd, addr, boardFilters(cmd)))
		},
	}
	serveCmd.Flags().String("addr", "127.0.0.1:7420", i18n.T("flag.addr"))
	serveCmd.Flags().String("project", "", i18n.T("flag.project"))
	serveCmd.Flags().String("agent", "", i18n.T("flag.agent_filter"))
	serveCmd.Flags().String("status", "", i18n.T("flag.status_filter"))
	root.AddCommand(serveCmd)

	statusCmd := &cobra.Command{Use: "status", Short: i18n.T("cmd.status.short")}
	statusReportCmd := &cobra.Command{
		Use:   "report",
		Short: i18n.T("cmd.status.report.short"),
		Run: func(cmd *cobra.Command, args []string) {
			msg, _ := cmd.Flags().GetString("message")
			task, _ := cmd.Flags().GetString("task")
			phase, _ := cmd.Flags().GetString("phase")
			client, _ := cmd.Flags().GetString("client")
			agent, _ := cmd.Flags().GetString("agent")
			project, _ := cmd.Flags().GetString("project")
			st, _ := cmd.Flags().GetString("status")
			logPath, _ := cmd.Flags().GetString("log-path")
			var pctPtr *int
			if cmd.Flags().Changed("pct") {
				p, _ := cmd.Flags().GetInt("pct")
				pctPtr = &p
			}
			printRes(app.StatusReport(cwd, app.StatusReportInput{
				ProjectID: project, Actor: agent, TaskID: task,
				Message: msg, Phase: phase, Pct: pctPtr, Client: client, Status: st,
				LogPath: logPath,
			}))
		},
	}
	statusReportCmd.Flags().StringP("message", "m", "", i18n.T("flag.message"))
	statusReportCmd.Flags().String("task", "", i18n.T("flag.task"))
	statusReportCmd.Flags().String("phase", "", i18n.T("flag.phase"))
	statusReportCmd.Flags().Int("pct", 0, i18n.T("flag.pct"))
	statusReportCmd.Flags().String("client", "", i18n.T("flag.client"))
	statusReportCmd.Flags().String("agent", "", i18n.T("flag.agent"))
	statusReportCmd.Flags().String("project", "", i18n.T("flag.project"))
	statusReportCmd.Flags().String("status", "", i18n.T("flag.agent_status"))
	statusReportCmd.Flags().String("log-path", "", i18n.T("flag.log_path"))
	statusCmd.AddCommand(statusReportCmd)
	root.AddCommand(statusCmd)
	root.AddCommand(&cobra.Command{Use: "doctor", Run: func(cmd *cobra.Command, args []string) { printRes(app.Doctor(cwd)) }})

	blockCmd := &cobra.Command{
		Use: "block",
		Run: func(cmd *cobra.Command, args []string) {
			msg, _ := cmd.Flags().GetString("message")
			task, _ := cmd.Flags().GetString("task")
			printRes(app.Block(cwd, msg, task))
		},
	}
	blockCmd.Flags().StringP("message", "m", "", i18n.T("flag.message"))
	_ = blockCmd.MarkFlagRequired("message")
	blockCmd.Flags().String("task", "", i18n.T("flag.task"))
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

	managedCmd := &cobra.Command{Use: "managed", Short: i18n.T("cmd.managed.short")}
	managedStartCmd := &cobra.Command{
		Use:   "start",
		Short: i18n.T("cmd.managed.start.short"),
		Run: func(cmd *cobra.Command, args []string) {
			task, _ := cmd.Flags().GetString("task")
			node, _ := cmd.Flags().GetString("node")
			agent, _ := cmd.Flags().GetString("agent")
			printRes(app.ManagedStart(cwd, task, node, agent, dryRun))
		},
	}
	managedStartCmd.Flags().String("task", "", i18n.T("flag.task"))
	managedStartCmd.Flags().String("node", "", i18n.T("flag.node"))
	managedStartCmd.Flags().String("agent", "", i18n.T("flag.agent"))
	managedCmd.AddCommand(managedStartCmd)
	managedCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: i18n.T("cmd.managed.status.short"),
		Run:   func(cmd *cobra.Command, args []string) { printRes(app.ManagedStatus(cwd)) },
	})
	root.AddCommand(managedCmd)

	// 同一二进制的内部 Runner：参数白名单，隐藏于帮助。
	runnerCmd := &cobra.Command{
		Use:    "__runner",
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			op, _ := cmd.Flags().GetString("operation")
			nonce, _ := cmd.Flags().GetString("nonce")
			stateDir, _ := cmd.Flags().GetString("state-dir")
			token, _ := cmd.Flags().GetString("token")
			once, _ := cmd.Flags().GetBool("once")
			printRes(app.InternalRunner(app.RunnerOpts{
				OperationID: op, Nonce: nonce, StateDir: stateDir, Token: token, Once: once,
			}))
		},
	}
	runnerCmd.Flags().String("operation", "", i18n.T("flag.runner_operation"))
	runnerCmd.Flags().String("nonce", "", i18n.T("flag.runner_nonce"))
	runnerCmd.Flags().String("state-dir", "", i18n.T("flag.runner_state_dir"))
	runnerCmd.Flags().String("token", "", i18n.T("flag.runner_token"))
	runnerCmd.Flags().Bool("once", false, "exit after bootstrap (test)")
	root.AddCommand(runnerCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
