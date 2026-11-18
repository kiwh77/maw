package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/kiwh77/maw/internal/app"
)

// leadGoalTree 构造只含 lead 命令的树，避免依赖 main() 里的整棵命令树。
func leadGoalTree(t *testing.T) (*cobra.Command, *string) {
	t.Helper()
	cwd := "."
	lead := &cobra.Command{Use: "lead"}
	root := &cobra.Command{Use: "maw"}
	root.AddCommand(lead)
	addLeadGoalCommands(lead, &cwd, func(app.Result, error) {})
	return root, &cwd
}

func findSub(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("缺少子命令 %q（现有：%v）", name, names(parent.Commands()))
	return nil
}

func names(cmds []*cobra.Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Name())
	}
	return out
}

func TestLeadGoalCommandsRegistered(t *testing.T) {
	root, _ := leadGoalTree(t)
	lead := findSub(t, root, "lead")
	goalCmd := findSub(t, lead, "goal")
	if goalCmd.Use != "goal" {
		t.Fatalf("goal 命令 use=%q", goalCmd.Use)
	}
	for _, name := range []string{"plan", "approve", "enqueue", "show"} {
		findSub(t, goalCmd, name)
	}
}

func TestLeadGoalPlanFlags(t *testing.T) {
	root, _ := leadGoalTree(t)
	plan := findSub(t, findSub(t, findSub(t, root, "lead"), "goal"), "plan")
	for _, flag := range []string{"goal", "title", "statement", "acceptance", "evidence", "kind", "rev", "max-cards", "iteration"} {
		if plan.Flags().Lookup(flag) == nil {
			t.Fatalf("plan 缺 flag --%s（现有：%v）", flag, flagNames(plan))
		}
	}
	approve := findSub(t, findSub(t, findSub(t, root, "lead"), "goal"), "approve")
	for _, flag := range []string{"goal", "rev", "policy", "auto-enqueue"} {
		if approve.Flags().Lookup(flag) == nil {
			t.Fatalf("approve 缺 flag --%s", flag)
		}
	}
	if got := approve.Flags().Lookup("policy").DefValue; got != "manual" {
		t.Fatalf("approve 默认策略应为 manual，得到 %q", got)
	}
	auto := approve.Flags().Lookup("auto-enqueue")
	if auto == nil || auto.DefValue != "false" {
		t.Fatalf("auto-enqueue 默认必须关闭: %+v", auto)
	}
}

func TestLeadGoalShowAcceptsPositionalGoal(t *testing.T) {
	root, _ := leadGoalTree(t)
	show := findSub(t, findSub(t, findSub(t, root, "lead"), "goal"), "show")
	// 不设 Args 校验：命令体在 --goal 为空时回退到 args[0]。
	if show.Args != nil {
		if err := show.Args(show, []string{"gl_pi001ax9"}); err != nil {
			t.Fatalf("show 应接受位置参数目标 id: %v", err)
		}
	}
}

func flagNames(c *cobra.Command) []string {
	out := []string{}
	c.Flags().VisitAll(func(f *pflag.Flag) { out = append(out, f.Name) })
	return out
}
