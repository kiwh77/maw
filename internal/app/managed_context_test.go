package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/journal"
)

// 非法 context 必须在起进程之前被拒绝，不能留下一条「跑过但上下文不明」的 Run。
func TestManagedLaunchContextRejectsIllegal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAW_RUN_CONTEXT", "")
	t.Setenv("MAW_CONTEXT_SUMMARY_SOURCE", "")
	spec := []byte(`{"executable":"/bin/echo","argv":["ok"],"context":"bogus"}`)
	if err := os.WriteFile(filepath.Join(dir, launchSpecFile), spec, 0o600); err != nil {
		t.Fatal(err)
	}
	_, wired, _, err := maybeRunSession(dir, "op_ctx_illegal")
	if err == nil {
		t.Fatal("非法 context 必须被拒绝")
	}
	if !wired {
		t.Fatal("已读到 launch_spec，应报告为装配失败而不是「没接线」")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Fatalf("错误信息应指向 context，实际 %q", err)
	}
	if _, e := os.Stat(filepath.Join(dir, "report.json")); !os.IsNotExist(e) {
		t.Fatal("拒绝起跑后不应产生执行报告")
	}
}

// fresh / fork 在冻结规格里可区分：fork 带摘要来源，fresh 序列化后不出现旧摘要字段。
func TestManagedLaunchContextFreshVsForkFrozenSpec(t *testing.T) {
	cases := []struct {
		name        string
		field       string
		wantMode    journal.RunContextMode
		wantSummary bool
	}{
		{"fresh", `"fresh"`, journal.RunContextFresh, false},
		{"fork", `{"mode":"fork","summary_source":"ti_prev","summary_ref":"run_prev"}`,
			journal.RunContextFork, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("MAW_RUN_CONTEXT", "")
			specJSON := []byte(`{"executable":"/bin/echo","argv":["ok"],"context":` + tc.field + `}`)
			ctx, err := journal.ResolveContext(dir, specJSON)
			if err != nil {
				t.Fatalf("解析上下文: %v", err)
			}
			if ctx.Mode != tc.wantMode {
				t.Fatalf("mode=%q，期望 %q", ctx.Mode, tc.wantMode)
			}
			var ls model.LaunchSpec
			if err := json.Unmarshal(specJSON, &ls); err != nil {
				t.Fatal(err)
			}
			jr, err := journal.Open(filepath.Join(dir, "journal"),
				journal.Options{RunID: "run_context_test", MaxBytes: journal.DefaultMaxBytes, Sync: true})
			if err != nil {
				t.Fatal(err)
			}
			defer jr.Close()
			if _, err := jr.WriteSpec(launchContextRecord{LaunchSpec: ls, Context: ctx}); err != nil {
				t.Fatalf("冻结规格: %v", err)
			}
			raw, _, err := jr.ReadSpec()
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Context journal.RunContextSpec `json:"context"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("冻结规格不是合法 JSON: %v", err)
			}
			if got.Context.Mode != tc.wantMode {
				t.Fatalf("冻结规格 mode=%q，期望 %q", got.Context.Mode, tc.wantMode)
			}
			hasSummary := strings.Contains(string(raw), "summary_source")
			if hasSummary != tc.wantSummary {
				t.Fatalf("summary_source 出现=%v，期望 %v；raw=%s", hasSummary, tc.wantSummary, raw)
			}
			if tc.wantSummary && got.Context.SummarySource != "ti_prev" {
				t.Fatalf("fork 摘要来源丢失: %+v", got.Context)
			}
		})
	}
}

// context.json 与 launch_spec 顶层字段都可达，且非法值不降级。
func TestManagedLaunchContextSourcesAndNoFallbackOnIllegal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAW_RUN_CONTEXT", "")
	if err := os.WriteFile(filepath.Join(dir, journal.ContextFile),
		[]byte(`{"mode":"fork","summary_source":"ti_prev"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := journal.ResolveContext(dir, []byte(`{"executable":"/bin/echo"}`))
	if err != nil || got.Mode != journal.RunContextFork {
		t.Fatalf("context.json 应生效: %+v / %v", got, err)
	}
	got, err = journal.ResolveContext(dir, []byte(`{"executable":"/bin/echo","context":"fresh"}`))
	if err != nil || got.Mode != journal.RunContextFresh {
		t.Fatalf("launch_spec 顶层应优先: %+v / %v", got, err)
	}
	if _, err := journal.ResolveContext(dir, []byte(`{"context":"inherit"}`)); err == nil {
		t.Fatal("非法顶层 context 不得降级到 context.json")
	}
}
