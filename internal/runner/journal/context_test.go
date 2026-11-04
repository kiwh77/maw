package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRunContextFreshDefault(t *testing.T) {
	for _, raw := range []string{"", "null", `"fresh"`, `{"mode":"fresh"}`} {
		got, err := ParseRunContext(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("raw=%q: 不该报错: %v", raw, err)
		}
		if got.Mode != RunContextFresh {
			t.Fatalf("raw=%q: mode=%q，期望 fresh", raw, got.Mode)
		}
		if got.SummarySource != "" || got.SummaryRef != "" {
			t.Fatalf("raw=%q: fresh 不应带旧摘要: %+v", raw, got)
		}
	}
}

func TestParseRunContextForkRequiresSummarySource(t *testing.T) {
	if _, err := ParseRunContext(json.RawMessage(`"fork"`)); err == nil {
		t.Fatal("裸 fork 缺少摘要来源，必须被拒绝")
	}
	got, err := ParseRunContext(json.RawMessage(
		`{"mode":"fork","summary_source":"ti_prev","summary_ref":"run_prev","requested_by":"lead"}`))
	if err != nil {
		t.Fatalf("合法 fork 不该报错: %v", err)
	}
	if got.Mode != RunContextFork || got.SummarySource != "ti_prev" || got.SummaryRef != "run_prev" {
		t.Fatalf("fork 决策字段丢失: %+v", got)
	}
}

func TestParseRunContextRejectsIllegalMode(t *testing.T) {
	for _, raw := range []string{`"bogus"`, `{"mode":"inherit"}`, `42`, `{"mode":7}`} {
		_, err := ParseRunContext(json.RawMessage(raw))
		if err == nil {
			t.Fatalf("raw=%q: 非法 context 必须被拒绝", raw)
		}
		if !strings.Contains(err.Error(), "context") {
			t.Fatalf("raw=%q: 错误信息应指明 context，实际 %q", raw, err)
		}
	}
}

func TestValidateFreshRejectsStaleSummary(t *testing.T) {
	s := RunContextSpec{Mode: RunContextFresh, SummarySource: "ti_prev"}
	if err := s.Validate(); err == nil {
		t.Fatal("fresh 携带旧摘要必须被拒绝")
	}
	// 零值按 fresh 处理，保证既有调用方不因新字段被破坏。
	if err := (RunContextSpec{}).Validate(); err != nil {
		t.Fatalf("零值 context 应视为 fresh: %v", err)
	}
}

func TestResolveContextPriorityAndFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAW_RUN_CONTEXT", "")
	t.Setenv("MAW_CONTEXT_SUMMARY_SOURCE", "")

	// 4. 全缺省 → fresh
	got, err := ResolveContext(dir, []byte(`{"executable":"/bin/echo"}`))
	if err != nil || got.Mode != RunContextFresh {
		t.Fatalf("缺省应为 fresh: %+v / %v", got, err)
	}
	// 2. context.json 优先于缺省
	if err := os.WriteFile(filepath.Join(dir, ContextFile),
		[]byte(`{"mode":"fork","summary_source":"ti_prev"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveContext(dir, []byte(`{"executable":"/bin/echo"}`))
	if err != nil || got.Mode != RunContextFork {
		t.Fatalf("context.json 应生效: %+v / %v", got, err)
	}
	// 1. launch_spec 顶层 context 优先级最高
	got, err = ResolveContext(dir, []byte(`{"executable":"/bin/echo","context":"fresh"}`))
	if err != nil || got.Mode != RunContextFresh {
		t.Fatalf("launch_spec 顶层 context 应覆盖文件: %+v / %v", got, err)
	}
	// 1. 非法值不降级、不再往下找
	if _, err := ResolveContext(dir, []byte(`{"context":"bogus"}`)); err == nil {
		t.Fatal("launch_spec 里的非法 context 必须拒绝，不能降级到 context.json")
	}
	// 3. 环境变量 fallback
	if err := os.Remove(filepath.Join(dir, ContextFile)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAW_RUN_CONTEXT", "fork")
	t.Setenv("MAW_CONTEXT_SUMMARY_SOURCE", "ti_env")
	got, err = ResolveContext(dir, []byte(`{"executable":"/bin/echo"}`))
	if err != nil || got.Mode != RunContextFork || got.SummarySource != "ti_env" {
		t.Fatalf("环境变量 fallback 失败: %+v / %v", got, err)
	}
}
