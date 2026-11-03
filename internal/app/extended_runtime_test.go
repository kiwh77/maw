package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	httpobservability "github.com/kiwh77/maw/internal/httpapi/observability"
)

// ---- 注册表本身 ----

// 注册表必须封闭：每个能力都有身份、级别与证据，ID 不重复，级别只有三种。
func TestExtensionRegistryIsClosed(t *testing.T) {
	seen := map[string]bool{}
	levels := map[ExtensionLevel]bool{
		ExtVerified: true, ExtExperimental: true, ExtUnsupported: true,
	}
	for _, spec := range Extensions() {
		if spec.ID == "" || spec.Name == "" {
			t.Fatalf("能力缺少身份或名称: %+v", spec)
		}
		if seen[spec.ID] {
			t.Fatalf("能力 ID 重复: %s", spec.ID)
		}
		seen[spec.ID] = true
		if !levels[spec.Level] {
			t.Fatalf("能力 %s 的级别不在封闭集合内: %q", spec.ID, spec.Level)
		}
		if spec.Evidence == "" {
			t.Fatalf("能力 %s 没有证据说明：无法解释为什么它开着", spec.ID)
		}
	}
	// 三条已验收闭环必须在表里，否则「关闭扩展仍保留人工与首个闭环」无从谈起。
	for _, id := range []string{"source.manual", "source.codebuddy", "observability.api"} {
		if !seen[id] {
			t.Fatalf("注册表缺少已验收能力 %s", id)
		}
	}
}

// ---- 证据决定上限 ----

// 未验证能力即使把扩展开关、自动执行、Git 工作区全部打开，也必须保持关闭。
func TestUnsupportedExtensionCannotBeForcedOn(t *testing.T) {
	t.Setenv(ExtendedEnvVar, "on")
	rt := &Runtime{pc: &projCtx{
		Repo:   gitDir(t),
		Config: &config.Config{Managed: config.Managed{AutoEnabled: true, Limit: 1}},
	}}
	states := rt.ExtensionStates()
	got, ok := findState(states, "dispatch.model_invoke")
	if !ok {
		t.Fatal("注册表缺少 dispatch.model_invoke")
	}
	if got.Enabled {
		t.Fatal("没有实测证据的能力被强行打开了")
	}
	if got.Reason != ReasonNoEvidence {
		t.Fatalf("原因码应为 %s，实得 %s", ReasonNoEvidence, got.Reason)
	}
}

// 关闭扩展开关只关实验性能力；人工与首个 CodeBuddy 闭环、只读观测 API 必须仍在。
func TestExtendedOffKeepsVerifiedCore(t *testing.T) {
	t.Setenv(ExtendedEnvVar, "off")
	rt := &Runtime{pc: &projCtx{
		Repo:   gitDir(t),
		Config: &config.Config{Managed: config.Managed{AutoEnabled: true, Limit: 1}},
	}}
	states := rt.ExtensionStates()

	for _, id := range []string{"source.manual", "source.codebuddy", "observability.api"} {
		st, ok := findState(states, id)
		if !ok || !st.Enabled {
			t.Fatalf("关闭扩展后已验收能力 %s 不应失活: %+v", id, st)
		}
	}
	for _, id := range []string{"source.zcode", "dispatch.auto", "workspace.nongit"} {
		st, ok := findState(states, id)
		if !ok {
			t.Fatalf("注册表缺少 %s", id)
		}
		if st.Enabled {
			t.Fatalf("关闭扩展后实验性能力 %s 仍开着", id)
		}
		if st.Reason != ReasonExtendedOff {
			t.Fatalf("%s 的原因码应为 %s，实得 %s", id, ReasonExtendedOff, st.Reason)
		}
	}
}

// 非 Git 工作区：依赖基线的来源与自动派发不能开，人工闭环不受影响。
func TestNonGitWorkspaceBlocksGitDependentExtensions(t *testing.T) {
	t.Setenv(ExtendedEnvVar, "on")
	dir := t.TempDir() // 故意不含 .git
	rt := &Runtime{pc: &projCtx{
		Repo:   dir,
		Config: &config.Config{Managed: config.Managed{AutoEnabled: true, Limit: 1}},
	}}
	states := rt.ExtensionStates()
	for _, id := range []string{"source.zcode", "source.trae", "source.workbuddy", "dispatch.auto"} {
		st, _ := findState(states, id)
		if st.Enabled {
			t.Fatalf("非 Git 工作区下 %s 不应开启", id)
		}
		if st.Reason != ReasonNotGit {
			t.Fatalf("%s 的原因码应为 %s，实得 %s", id, ReasonNotGit, st.Reason)
		}
	}
	if st, _ := findState(states, "source.manual"); !st.Enabled {
		t.Fatal("人工闭环不该受工作区类型影响")
	}
}

// 自动执行默认关闭时，自动派发不能借扩展开关打开。
func TestAutoDispatchStaysOffWhenAutoDisabled(t *testing.T) {
	t.Setenv(ExtendedEnvVar, "on")
	rt := &Runtime{pc: &projCtx{
		Repo:   gitDir(t),
		Config: &config.Config{Managed: config.Managed{AutoEnabled: false, Limit: 1}},
	}}
	st, ok := findState(rt.ExtensionStates(), "dispatch.auto")
	if !ok {
		t.Fatal("注册表缺少 dispatch.auto")
	}
	if st.Enabled {
		t.Fatal("auto_enabled=false 时自动派发被打开了")
	}
	// 平台不支持的机器先报平台，这是更基础的原因；两者都算「没开」。
	if st.Reason != ReasonAutoDisabled && st.Reason != ReasonPlatform {
		t.Fatalf("原因码应为 %s 或 %s，实得 %s", ReasonAutoDisabled, ReasonPlatform, st.Reason)
	}
	if runtime.GOOS == "darwin" && st.Reason != ReasonAutoDisabled {
		t.Fatalf("macOS 上应因自动关闭而关，实得 %s", st.Reason)
	}
}

// 未注册的能力 ID 一律视为关闭（失败安全）。
func TestUnknownExtensionIsDisabled(t *testing.T) {
	rt := &Runtime{pc: &projCtx{Repo: gitDir(t), Config: &config.Config{}}}
	if rt.ExtensionEnabled("source.does-not-exist") {
		t.Fatal("未注册能力不应开启")
	}
	var nilRT *Runtime
	if nilRT.ExtensionEnabled("source.manual") {
		t.Fatal("nil Runtime 不应报告任何能力开启")
	}
}

// ---- 路由分流 ----

func TestV1RouteDispatchesObservability(t *testing.T) {
	cases := map[string]v1HandlerKind{
		"/api/v1/events":            v1Events,
		"/api/v1/events/stream":     v1Events,
		"/api/v1/status-reports":    v1Events,
		"/api/v1/board":             v1Read,
		"/api/v1/projects/p1/tasks": v1Read,
		"/api/v1/runs/run_1":        v1Read,
		"/api/v1/projects":          v1Read,

		// 工程本身（末段就是 {project_id}）走写资源；子资源仍按原规则分流。
		"/api/v1/projects/p1": v1Resources,

		"/api/v1/projects/p1/timeline":                   v1Observability,
		"/api/v1/projects/p1/activity":                   v1Observability,
		"/api/v1/projects/p1/sessions":                   v1Observability,
		"/api/v1/projects/p1/analytics":                  v1Observability,
		"/api/v1/projects/p1/notifications":              v1Observability,
		"/api/v1/projects/p1/notifications/n1/acks":      v1Observability,
		"/api/v1/projects/p1/observability/capabilities": v1Observability,
		"/api/v1/projects/p1/observability/stream":       v1Observability,

		"/api/v1/system-jobs":                v1System,
		"/api/v1/projects/p1/system-jobs":    v1System,
	}
	for path, want := range cases {
		if got := v1Route(path); got != want {
			t.Fatalf("%s 分流错误：want %v got %v", path, want, got)
		}
	}
}

// ---- 投影水位可访问，且缺测不冒充 0 ----

func TestTimelineReportsMissingInsteadOfZero(t *testing.T) {
	cases := []struct {
		name     string
		tl       checkpointTimeline
		project  string
		wantReas string
	}{
		{
			name:     "没有检查点来源",
			tl:       checkpointTimeline{},
			project:  "p1",
			wantReas: "projection_source_not_configured",
		},
		{
			name: "工程还没有检查点",
			tl: checkpointTimeline{Load: func(context.Context, string) (*db.ProjectionState, error) {
				return nil, nil
			}},
			project:  "p1",
			wantReas: "projection_not_built",
		},
	}
	for _, c := range cases {
		data, err := c.tl.Timeline(context.Background(), httpobservability.TimelineQuery{ProjectID: c.project})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(data.Missing) == 0 {
			t.Fatalf("%s: 缺测没有出现在 Missing 里，等于用空集冒充「没活动」", c.name)
		}
		if data.Missing[0].Reason != c.wantReas {
			t.Fatalf("%s: 原因码应为 %s，实得 %s", c.name, c.wantReas, data.Missing[0].Reason)
		}
		if len(data.Intervals) != 0 {
			t.Fatalf("%s: 没有检查点时不该编造区间", c.name)
		}
	}
}

func TestTimelineEchoesCheckpointCursors(t *testing.T) {
	tl := checkpointTimeline{Load: func(context.Context, string) (*db.ProjectionState, error) {
		return &db.ProjectionState{
			Projection: "timeline", ProjectID: "p1", Version: 2,
			Cursor: 4200, CompleteBefore: 4100,
			Meta: map[string]any{"completeness": "partial"},
		}, nil
	}}
	data, err := tl.Timeline(context.Background(), httpobservability.TimelineQuery{ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if data.Cursors.StateCursor != 4200 || data.Cursors.ProjectionCursor != 4100 {
		t.Fatalf("水位没有按检查点回显: %+v", data.Cursors)
	}
	if string(data.Cursors.Completeness) != "partial" {
		t.Fatalf("完整度应为 partial，实得 %q", data.Cursors.Completeness)
	}
	// 区间没重建这件事必须说出来，不能让人以为 0 条区间就是没干活。
	found := false
	for _, m := range data.Missing {
		if m.Reason == "projection_not_rebuilt" {
			found = true
		}
	}
	if !found {
		t.Fatal("缺少「区间未重建」的缺测说明")
	}
}

func TestTimelineRequiresProject(t *testing.T) {
	data, err := checkpointTimeline{}.Timeline(context.Background(), httpobservability.TimelineQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Unsupported) == 0 || data.Unsupported[0].Reason != "project_required" {
		t.Fatalf("缺 project_id 时应报不支持而不是猜: %+v", data.Unsupported)
	}
}

// ---- 装配后的接口真的能答 ----

// 观测子树装配完必须能应答：时间轴给出水位语义（本卡：缺测如实报），
// 提醒列表与能力清单可读。装配漏一个适配器的症状就是这里 404／500。
func TestObservabilityHandlerAnswers(t *testing.T) {
	store, gdb := tempStore(t)
	defer store.Close()
	_ = gdb

	h := newObservabilityAPI(store, nil).Handler()

	for _, path := range []string{
		"/api/v1/projects/p1/timeline?window=24h",
		"/api/v1/projects/p1/activity?window=24h",
		"/api/v1/projects/p1/analytics?window=24h",
		"/api/v1/projects/p1/notifications?window=24h",
		"/api/v1/projects/p1/observability/capabilities",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应可访问，实得 %d：%s", path, rec.Code, rec.Body.String())
		}
	}
}

// 提醒确认是写操作：没有写事务适配器时必须报「不支持」，不能静默成功。
func TestObservabilityAckRequiresWriter(t *testing.T) {
	store, _ := tempStore(t)
	defer store.Close()
	api := newObservabilityAPI(store, nil)
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"at":"2026-10-24T00:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/projects/p1/notifications/n1/acks", body)
	api.Handler().ServeHTTP(rec, req)
	// 不存在的提醒就该 404/400/409 之类，但绝不能是「假装确认成功」的 2xx。
	if rec.Code >= 200 && rec.Code < 300 {
		t.Fatalf("对不存在的提醒确认不该成功: %d %s", rec.Code, rec.Body.String())
	}
}

// tempStore 在临时目录开一个真实库（迁移已跑过），供装配测试使用。
func tempStore(t *testing.T) (*db.Store, string) {
	t.Helper()
	gdb := filepath.Join(t.TempDir(), "maw.db")
	if err := db.Init(gdb); err != nil {
		t.Fatalf("初始化临时库: %v", err)
	}
	store, err := db.OpenStore(gdb)
	if err != nil {
		t.Fatalf("打开临时库: %v", err)
	}
	return store, gdb
}

// ---- 辅助 ----

// gitDir 造一个带 .git 的临时目录（只造标记，不初始化仓库）。
func gitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func findState(states []ExtensionState, id string) (ExtensionState, bool) {
	for _, st := range states {
		if st.ID == id {
			return st, true
		}
	}
	return ExtensionState{}, false
}
