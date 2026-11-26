package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

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
		for _, k := range []string{"home", "global_db", "project_id", "project_name", "project_path", "worktree", "branch", "task_id", "status", "board", "html", "agent", "registered", "role", "url", "log_path", "protocol_fallback", "worktrees", "branches", "removed_worktrees", "removed_branches"} {
			if v, ok := r[k]; ok {
				fmt.Printf("%s: %v\n", k, v)
			}
		}
		// 工程清单：id／名称／路径三列比 JSON 一行好读，也便于复制作参。
		if items, ok := r["projects"].([]app.Result); ok && len(items) > 0 {
			for _, item := range items {
				fmt.Printf("  %v\t%v\t%v\n", item["project_id"], item["project_name"], item["project_path"])
			}
		}
		if s, ok := r["summary"].(string); ok && s != "" {
			fmt.Println(s)
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
			allowRetire, _ := cmd.Flags().GetBool("allow-retire")
			printRes(app.PlanApplyWith(args[0], cwd, app.PlanApplyOptions{
				Yes: yes, DryRun: dryRun, Reset: reset, AllowRetire: allowRetire,
			}))
		},
	})
	planCmd.Commands()[0].Flags().Bool("yes", false, i18n.T("flag.yes"))
	planCmd.Commands()[0].Flags().Bool("reset", false, i18n.T("flag.reset"))
	planCmd.Commands()[0].Flags().Bool("allow-retire", false, i18n.T("flag.allow_retire"))
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
			claim, _ := cmd.Flags().GetBool("claim")
			lane, _ := cmd.Flags().GetString("lane")
			releaseLane, _ := cmd.Flags().GetBool("release-lane")
			task, _ := cmd.Flags().GetString("task")
			printRes(app.NextWith(cwd, agent, app.NextOptions{
				DryRun: dryRun, LogPath: logPath, Claim: claim,
				Lane: lane, ReleaseLane: releaseLane, TaskID: task,
			}))
		},
	}
	nextCmd.Flags().String("agent", "", i18n.T("flag.agent"))
	nextCmd.Flags().String("log-path", "", i18n.T("flag.log_path"))
	nextCmd.Flags().Bool("claim", false, i18n.T("flag.claim"))
	nextCmd.Flags().String("lane", "", i18n.T("flag.lane"))
	nextCmd.Flags().Bool("release-lane", false, i18n.T("flag.release_lane"))
	nextCmd.Flags().String("task", "", "精确领取指定 ready 任务")
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

	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: i18n.T("cmd.sync.short"),
		Run: func(cmd *cobra.Command, args []string) {
			task, _ := cmd.Flags().GetString("task")
			printRes(app.Sync(cwd, task))
		},
	}
	syncCmd.Flags().String("task", "", i18n.T("flag.task"))
	root.AddCommand(syncCmd)

	reqChangeCmd := &cobra.Command{
		Use:   "request-change",
		Short: i18n.T("cmd.request_change.short"),
		Run: func(cmd *cobra.Command, args []string) {
			task, _ := cmd.Flags().GetString("task")
			msg, _ := cmd.Flags().GetString("message")
			adds, _ := cmd.Flags().GetStringSlice("add")
			printRes(app.RequestChange(cwd, app.RequestChangeOptions{
				TaskID: task, Add: adds, Message: msg, DryRun: dryRun,
			}))
		},
	}
	reqChangeCmd.Flags().String("task", "", i18n.T("flag.task"))
	reqChangeCmd.Flags().StringP("message", "m", "", i18n.T("flag.message"))
	reqChangeCmd.Flags().StringSlice("add", nil, i18n.T("flag.scope_add"))
	root.AddCommand(reqChangeCmd)

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
			abort, _ := cmd.Flags().GetBool("abort")
			tasks, _ := cmd.Flags().GetStringSlice("task")
			printRes(app.MergeWith(cwd, app.MergeOptions{
				Yes: yes, DryRun: dryRun, TaskIDs: tasks, Abort: abort,
			}))
		},
	}
	mergeCmd.Flags().Bool("yes", false, i18n.T("flag.yes"))
	mergeCmd.Flags().Bool("abort", false, "中止 conflict/merge-blocked，退回 in_progress（需 --task）")
	mergeCmd.Flags().StringSlice("task", nil, "只合入指定任务（可重复）")
	root.AddCommand(mergeCmd)

	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: i18n.T("cmd.prune.short"),
		Run: func(cmd *cobra.Command, args []string) {
			yes, _ := cmd.Flags().GetBool("yes")
			history, _ := cmd.Flags().GetBool("history")
			older, _ := cmd.Flags().GetInt("older-than-hours")
			forceUnmerged, _ := cmd.Flags().GetBool("force-unmerged")
			if history {
				printRes(app.CleanupHistory(cwd, app.CleanupHistoryOptions{
					Yes: yes, DryRun: dryRun, OlderThanHours: older,
				}))
				return
			}
			printRes(app.PruneWith(cwd, app.PruneOptions{
				Yes: yes, DryRun: dryRun, ForceUnmerged: forceUnmerged,
			}))
		},
	}
	pruneCmd.Flags().Bool("yes", false, i18n.T("flag.yes_delete"))
	pruneCmd.Flags().Bool("force-unmerged", false, "允许删除 tip 尚未合入主干的 agent 分支")
	pruneCmd.Flags().Bool("history", false, "清理已消化的历史 block 事件与过期 unblock_assist（先导出 JSON）")
	pruneCmd.Flags().Int("older-than-hours", 24, "history 模式下终态 unblock_assist 保留龄（小时）")
	root.AddCommand(pruneCmd)

	reconcileCmd := &cobra.Command{
		Use:   "reconcile",
		Short: i18n.T("cmd.reconcile.short"),
		Run: func(cmd *cobra.Command, args []string) {
			apply, _ := cmd.Flags().GetBool("apply")
			syncCheckout, _ := cmd.Flags().GetBool("sync-checkout")
			printRes(app.Reconcile(cwd, app.ReconcileOptions{
				Apply: apply, SyncCheckout: syncCheckout, DryRun: dryRun,
			}))
		},
	}
	reconcileCmd.Flags().Bool("apply", false, i18n.T("flag.reconcile_apply"))
	reconcileCmd.Flags().Bool("sync-checkout", false, i18n.T("flag.sync_checkout"))
	root.AddCommand(reconcileCmd)

	leaseCmd := &cobra.Command{Use: "lease", Short: i18n.T("cmd.lease.short")}
	leaseCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: i18n.T("cmd.lease.list.short"),
		Run:   func(cmd *cobra.Command, args []string) { printRes(app.LeaseList(cwd)) },
	})
	leaseReleaseCmd := &cobra.Command{
		Use:   "release",
		Short: i18n.T("cmd.lease.release.short"),
		Run: func(cmd *cobra.Command, args []string) {
			yes, _ := cmd.Flags().GetBool("yes")
			leaseID, _ := cmd.Flags().GetString("lease")
			task, _ := cmd.Flags().GetString("task")
			printRes(app.LeaseRelease(cwd, app.LeaseReleaseOptions{
				LeaseID: leaseID, TaskID: task, Yes: yes, DryRun: dryRun,
			}))
		},
	}
	leaseReleaseCmd.Flags().Bool("yes", false, i18n.T("flag.yes"))
	leaseReleaseCmd.Flags().String("lease", "", "lease id")
	leaseReleaseCmd.Flags().String("task", "", i18n.T("flag.task"))
	leaseCmd.AddCommand(leaseReleaseCmd)
	root.AddCommand(leaseCmd)

	configCmd := &cobra.Command{Use: "config", Short: i18n.T("cmd.config.short")}
	configCmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: i18n.T("cmd.config.path.short"),
		Run:   func(cmd *cobra.Command, args []string) { printRes(app.ConfigPath(cwd)) },
	})
	root.AddCommand(configCmd)

	boardFilters := func(cmd *cobra.Command) app.BoardFilters {
		return app.BoardFilters{
			Project:   mustFlag(cmd, "project"),
			Agent:     mustFlag(cmd, "agent"),
			Status:    mustFlag(cmd, "status"),
			Iteration: mustFlag(cmd, "iteration"),
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
	boardCmd.Flags().String("iteration", "", "按 iteration_id 过滤")
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
	serveCmd.Flags().String("iteration", "", "按 iteration_id 过滤")
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
			kind, _ := cmd.Flags().GetString("kind")
			var pctPtr *int
			if cmd.Flags().Changed("pct") {
				p, _ := cmd.Flags().GetInt("pct")
				pctPtr = &p
			}
			printRes(app.StatusReport(cwd, app.StatusReportInput{
				ProjectID: project, Actor: agent, TaskID: task,
				Message: msg, Phase: phase, Pct: pctPtr, Client: client, Status: st,
				LogPath: logPath, Kind: kind,
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
	statusReportCmd.Flags().String("kind", "", i18n.T("flag.kind"))
	statusCmd.AddCommand(statusReportCmd)
	root.AddCommand(statusCmd)

	statsCmd := &cobra.Command{Use: "stats", Short: i18n.T("cmd.stats.short")}
	statsReportCmd := &cobra.Command{
		Use:   "report",
		Short: i18n.T("cmd.stats.report.short"),
		Run: func(cmd *cobra.Command, args []string) {
			task, _ := cmd.Flags().GetString("task")
			agent, _ := cmd.Flags().GetString("agent")
			project, _ := cmd.Flags().GetString("project")
			client, _ := cmd.Flags().GetString("client")
			modelName, _ := cmd.Flags().GetString("model")
			session, _ := cmd.Flags().GetString("session")
			source, _ := cmd.Flags().GetString("source")
			turns, _ := cmd.Flags().GetInt("turns")
			duration, _ := cmd.Flags().GetInt("duration")
			quality, _ := cmd.Flags().GetString("quality")
			startedAt, _ := cmd.Flags().GetString("started-at")
			var input, output, cacheRead, cacheWrite *int64
			if cmd.Flags().Changed("input-tokens") {
				v, _ := cmd.Flags().GetInt64("input-tokens")
				input = &v
			}
			if cmd.Flags().Changed("output-tokens") {
				v, _ := cmd.Flags().GetInt64("output-tokens")
				output = &v
			}
			if cmd.Flags().Changed("cache-read-tokens") {
				v, _ := cmd.Flags().GetInt64("cache-read-tokens")
				cacheRead = &v
			}
			if cmd.Flags().Changed("cache-write-tokens") {
				v, _ := cmd.Flags().GetInt64("cache-write-tokens")
				cacheWrite = &v
			}
			printRes(app.StatsReport(cwd, app.StatsReportInput{
				ProjectID: project, Actor: agent, TaskID: task,
				Client: client, Model: modelName, SessionID: session, SourceNS: source,
				Turns: turns, InputTokens: input, OutputTokens: output,
				CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
				DurationSeconds: duration, StartedAt: startedAt, Quality: quality,
			}))
		},
	}
	statsReportCmd.Flags().String("task", "", i18n.T("flag.task"))
	statsReportCmd.Flags().String("agent", "", i18n.T("flag.agent"))
	statsReportCmd.Flags().String("project", "", i18n.T("flag.project"))
	statsReportCmd.Flags().String("client", "", i18n.T("flag.stats_client"))
	statsReportCmd.Flags().String("model", "", i18n.T("flag.stats_model"))
	statsReportCmd.Flags().String("session", "", i18n.T("flag.stats_session"))
	statsReportCmd.Flags().String("source", "", i18n.T("flag.stats_source"))
	statsReportCmd.Flags().Int("turns", 1, i18n.T("flag.stats_turns"))
	statsReportCmd.Flags().Int("duration", 0, i18n.T("flag.stats_duration"))
	statsReportCmd.Flags().Int64("input-tokens", 0, i18n.T("flag.stats_input_tokens"))
	statsReportCmd.Flags().Int64("output-tokens", 0, i18n.T("flag.stats_output_tokens"))
	statsReportCmd.Flags().Int64("cache-read-tokens", 0, i18n.T("flag.stats_cache_read"))
	statsReportCmd.Flags().Int64("cache-write-tokens", 0, i18n.T("flag.stats_cache_write"))
	statsReportCmd.Flags().String("quality", "", i18n.T("flag.stats_quality"))
	statsReportCmd.Flags().String("started-at", "", i18n.T("flag.stats_started_at"))
	statsCmd.AddCommand(statsReportCmd)
	root.AddCommand(statsCmd)

	// wait：等 deps 或等任务终态，超时用退出码 2 与「参数/环境错误」区分开。
	waitCmd := &cobra.Command{
		Use:   "wait",
		Short: i18n.T("cmd.wait.short"),
		Run: func(cmd *cobra.Command, args []string) {
			task, _ := cmd.Flags().GetString("task")
			depsOf, _ := cmd.Flags().GetString("deps-of")
			timeoutSec, _ := cmd.Flags().GetInt("timeout")
			pollSec, _ := cmd.Flags().GetInt("poll")
			printRes(app.Wait(cwd, app.WaitInput{
				TaskID:  task,
				DepsOf:  depsOf,
				Timeout: time.Duration(timeoutSec) * time.Second,
				Poll:    time.Duration(pollSec) * time.Second,
			}))
		},
	}
	waitCmd.Flags().String("task", "", i18n.T("flag.wait_task"))
	waitCmd.Flags().String("deps-of", "", i18n.T("flag.deps_of"))
	waitCmd.Flags().Int("timeout", 300, i18n.T("flag.wait_timeout"))
	waitCmd.Flags().Int("poll", 2, i18n.T("flag.wait_poll"))
	root.AddCommand(waitCmd)

	// reassign：中枢把任务改派给另一个已注册 Agent；必须带期望版本。
	reassignCmd := &cobra.Command{
		Use:   "reassign",
		Short: i18n.T("cmd.reassign.short"),
		Run: func(cmd *cobra.Command, args []string) {
			task, _ := cmd.Flags().GetString("task")
			to, _ := cmd.Flags().GetString("to")
			rev, _ := cmd.Flags().GetInt("revision")
			if !cmd.Flags().Changed("revision") {
				rev = -1
			}
			printRes(app.Reassign(cwd, app.ReassignInput{
				TaskID: task, To: to, ExpectedRevision: rev,
			}))
		},
	}
	reassignCmd.Flags().String("task", "", i18n.T("flag.reassign_task"))
	reassignCmd.Flags().String("to", "", i18n.T("flag.reassign_to"))
	reassignCmd.Flags().Int("revision", -1, i18n.T("flag.revision"))
	root.AddCommand(reassignCmd)

	// interrupt：只有 Lead/中枢能喊停别人的执行；普通成员越权会被 not_lead 拒绝。
	interruptCmd := &cobra.Command{
		Use:   "interrupt",
		Short: i18n.T("cmd.interrupt.short"),
		Run: func(cmd *cobra.Command, args []string) {
			task, _ := cmd.Flags().GetString("task")
			reason, _ := cmd.Flags().GetString("reason")
			rev, _ := cmd.Flags().GetInt("revision")
			if !cmd.Flags().Changed("revision") {
				rev = -1
			}
			printRes(app.Interrupt(cwd, app.InterruptInput{
				TaskID: task, ExpectedRevision: rev, Reason: reason,
			}))
		},
	}
	interruptCmd.Flags().String("task", "", i18n.T("flag.reassign_task"))
	interruptCmd.Flags().String("reason", "", i18n.T("flag.interrupt_reason"))
	interruptCmd.Flags().Int("revision", -1, i18n.T("flag.revision"))
	root.AddCommand(interruptCmd)
	root.AddCommand(&cobra.Command{Use: "doctor", Run: func(cmd *cobra.Command, args []string) { printRes(app.Doctor(cwd)) }})

	blockCmd := &cobra.Command{
		Use: "block",
		Run: func(cmd *cobra.Command, args []string) {
			msg, _ := cmd.Flags().GetString("message")
			task, _ := cmd.Flags().GetString("task")
			agent, _ := cmd.Flags().GetString("agent")
			printRes(app.BlockWith(cwd, msg, task, agent))
		},
	}
	blockCmd.Flags().StringP("message", "m", "", i18n.T("flag.message"))
	_ = blockCmd.MarkFlagRequired("message")
	blockCmd.Flags().String("task", "", i18n.T("flag.task"))
	blockCmd.Flags().String("agent", "", "按 Agent 当前持有题推断 task（主工作区可用）")
	root.AddCommand(blockCmd)

	// 工程登记管理：看板「工程」页与 CLI 共用同一套移除语义。
	projectCmd := &cobra.Command{
		Use:   "project",
		Short: i18n.T("cmd.project.short"),
	}
	projectCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: i18n.T("cmd.project.list.short"),
		Run:   func(cmd *cobra.Command, args []string) { printRes(app.ProjectList(cwd)) },
	})
	projectCmd.AddCommand(&cobra.Command{
		Use:   "add",
		Short: "登记当前 git 仓到本机 maw（仓外 home，不写工程文件）",
		Run:   func(cmd *cobra.Command, args []string) { printRes(app.ProjectAdd(cwd)) },
	})
	projectRmCmd := &cobra.Command{
		Use:   "rm [id|名称]",
		Short: i18n.T("cmd.project.rm.short"),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			force, _ := cmd.Flags().GetBool("force")
			agent, _ := cmd.Flags().GetString("agent")
			printRes(app.RemoveProject(cwd, args[0], app.ProjectRemovalOptions{
				Force:  force,
				DryRun: dryRun,
				Actor:  agent,
			}))
		},
	}
	projectRmCmd.Flags().Bool("force", false, i18n.T("flag.force_remove"))
	projectRmCmd.Flags().String("agent", "", i18n.T("flag.agent"))
	projectCmd.AddCommand(projectRmCmd)
	projectSetCmd := &cobra.Command{
		Use:   "set [id|名称]",
		Short: "更新工程名称、路径与执行策略",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			in := app.ProjectUpdateInput{Ref: args[0], DryRun: dryRun}
			if cmd.Flags().Changed("name") {
				v, _ := cmd.Flags().GetString("name")
				in.Name = &v
			}
			if cmd.Flags().Changed("path") {
				v, _ := cmd.Flags().GetString("path")
				in.Path = &v
			}
			if cmd.Flags().Changed("main-branch") {
				v, _ := cmd.Flags().GetString("main-branch")
				in.MainBranch = &v
			}
			if cmd.Flags().Changed("max-parallel") {
				v, _ := cmd.Flags().GetInt("max-parallel")
				in.MaxParallel = &v
			}
			printRes(app.ProjectUpdate(cwd, in))
		},
	}
	projectSetCmd.Flags().String("name", "", "工程显示名")
	projectSetCmd.Flags().String("path", "", "工程存放路径")
	projectSetCmd.Flags().String("main-branch", "", "主分支")
	projectSetCmd.Flags().Int("max-parallel", 0, "max_parallel 参考值")
	projectCmd.AddCommand(projectSetCmd)
	root.AddCommand(projectCmd)

	taskCmd := &cobra.Command{Use: "task", Short: i18n.T("cmd.task.short")}
	taskListCmd := &cobra.Command{
		Use: "list",
		Run: func(cmd *cobra.Command, args []string) {
			status, _ := cmd.Flags().GetString("status")
			iteration, _ := cmd.Flags().GetString("iteration")
			printRes(app.TaskListWith(cwd, app.TaskListOptions{
				Status: status, Iteration: iteration,
			}))
		},
	}
	taskListCmd.Flags().String("status", "", i18n.T("flag.status_filter"))
	taskListCmd.Flags().String("iteration", "", "按 iteration_id 过滤")
	taskCmd.AddCommand(taskListCmd)
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
	taskCancelCmd := &cobra.Command{
		Use:   "cancel [id]",
		Short: i18n.T("cmd.task.cancel.short"),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			reason, _ := cmd.Flags().GetString("reason")
			superseded, _ := cmd.Flags().GetString("superseded-by")
			printRes(app.TaskCancel(cwd, args[0], app.CancelOptions{
				Reason: reason, SupersededBy: superseded,
			}))
		},
	}
	taskCancelCmd.Flags().String("reason", "", i18n.T("flag.cancel_reason"))
	taskCancelCmd.Flags().String("superseded-by", "", i18n.T("flag.superseded_by"))
	taskCmd.AddCommand(taskCancelCmd)
	root.AddCommand(taskCmd)

	iterationCmd := &cobra.Command{Use: "iteration", Short: i18n.T("cmd.iteration.short")}
	iterationCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: i18n.T("cmd.iteration.list.short"),
		Run:   func(cmd *cobra.Command, args []string) { printRes(app.IterationList(cwd)) },
	})
	iterationCreateCmd := &cobra.Command{
		Use:   "create",
		Short: i18n.T("cmd.iteration.create.short"),
		Run: func(cmd *cobra.Command, args []string) {
			id, _ := cmd.Flags().GetString("id")
			name, _ := cmd.Flags().GetString("name")
			goal, _ := cmd.Flags().GetString("goal")
			target, _ := cmd.Flags().GetString("target-branch")
			status, _ := cmd.Flags().GetString("status")
			printRes(app.IterationCreate(cwd, app.IterationCreateInput{
				ID: id, Name: name, Goal: goal, TargetBranch: target,
				Status: status, DryRun: dryRun,
			}))
		},
	}
	iterationCreateCmd.Flags().String("id", "", i18n.T("flag.iteration_id"))
	iterationCreateCmd.Flags().String("name", "", i18n.T("flag.iteration_name"))
	iterationCreateCmd.Flags().String("goal", "", i18n.T("flag.iteration_goal"))
	iterationCreateCmd.Flags().String("target-branch", "", i18n.T("flag.iteration_target_branch"))
	iterationCreateCmd.Flags().String("status", "planning", i18n.T("flag.iteration_status"))
	iterationCmd.AddCommand(iterationCreateCmd)
	iterationCmd.AddCommand(&cobra.Command{
		Use:   "show [id]",
		Short: i18n.T("cmd.iteration.show.short"),
		Args:  cobra.ExactArgs(1),
		Run:   func(cmd *cobra.Command, args []string) { printRes(app.IterationShow(cwd, args[0])) },
	})
	iterationCloseCmd := &cobra.Command{
		Use:   "close [id]",
		Short: i18n.T("cmd.iteration.close.short"),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			status, _ := cmd.Flags().GetString("status")
			commit, _ := cmd.Flags().GetString("merge-commit")
			force, _ := cmd.Flags().GetBool("force")
			printRes(app.IterationClose(cwd, app.IterationCloseInput{
				ID: args[0], Status: status, MergeCommit: commit,
				Force: force, DryRun: dryRun,
			}))
		},
	}
	iterationCloseCmd.Flags().String("status", "merged", i18n.T("flag.iteration_close_status"))
	iterationCloseCmd.Flags().String("merge-commit", "", i18n.T("flag.iteration_merge_commit"))
	iterationCloseCmd.Flags().Bool("force", false, i18n.T("flag.iteration_force"))
	iterationCmd.AddCommand(iterationCloseCmd)
	root.AddCommand(iterationCmd)

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
