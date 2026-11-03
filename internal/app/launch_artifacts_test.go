package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/engine/dispatch"
	"github.com/kiwh77/maw/internal/engine/model"
)

// launchOp 是 Launch 前一刻的操作身份：字段与 dispatch.Operation 一致。
func launchOp(pc *projCtx) dispatch.Operation {
	return dispatch.Operation{
		OperationID: model.OperationID("op_launch01"),
		ProjectID:   model.ProjectID(pc.ID()),
		RunID:       model.RunID("run_launch01"),
		Instance:    model.InstanceID("ti_launch01"),
		Generation:  model.Generation(2),
	}
}

func readRunMeta(t *testing.T, opDir string) RunMeta {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(opDir, runMetaFile))
	if err != nil {
		t.Fatalf("run_meta.json 缺失: %v", err)
	}
	var meta RunMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("run_meta.json 不是合法 JSON: %v", err)
	}
	return meta
}

func readLaunchSpec(t *testing.T, opDir string) model.LaunchSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(opDir, launchSpecFile))
	if err != nil {
		t.Fatalf("launch_spec.json 缺失: %v", err)
	}
	var spec model.LaunchSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("launch_spec.json 不是合法 JSON: %v", err)
	}
	return spec
}

// 等价于一次 managed start 的准备阶段：Launch 之前把两份文件写到 ops/<op>/。
func TestPrepareRunnerArtifactsWritesRunMetaAndSpec(t *testing.T) {
	f := newFixture(t)
	rt, err := NewRuntime(f.pc, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(managedLaunchSpecEnv, "")
	t.Setenv(managedLaunchCmdEnv, `["/usr/bin/env","echo","hi"]`)

	op := launchOp(f.pc)
	opDir := rt.OpDir(string(op.OperationID))
	if err := rt.PrepareRunnerArtifacts(opDir, op); err != nil {
		t.Fatalf("PrepareRunnerArtifacts: %v", err)
	}

	meta := readRunMeta(t, opDir)
	if meta.RunID != string(op.RunID) {
		t.Fatalf("run_id=%q want %q", meta.RunID, op.RunID)
	}
	if meta.ProjectID != f.pc.ID() {
		t.Fatalf("project_id=%q want %q", meta.ProjectID, f.pc.ID())
	}
	if meta.GlobalDB == "" || meta.GlobalDB != f.pc.GlobalDB {
		t.Fatalf("global_db=%q want %q", meta.GlobalDB, f.pc.GlobalDB)
	}
	if meta.InstanceID != string(op.Instance) || meta.Generation != int(op.Generation) {
		t.Fatalf("实例/代际应取自操作: %+v", meta)
	}
	if meta.OperationID != string(op.OperationID) {
		t.Fatalf("operation_id=%q", meta.OperationID)
	}

	spec := readLaunchSpec(t, opDir)
	if spec.Executable != "/usr/bin/env" {
		t.Fatalf("executable=%q", spec.Executable)
	}
	if len(spec.Argv) != 2 || spec.Argv[0] != "echo" || spec.Argv[1] != "hi" {
		t.Fatalf("argv=%v", spec.Argv)
	}
	if !filepath.IsAbs(spec.Cwd) {
		t.Fatalf("cwd 应为绝对路径: %q", spec.Cwd)
	}
	if spec.StdinMode != model.StdinNone {
		t.Fatalf("stdin_mode=%q want %q", spec.StdinMode, model.StdinNone)
	}
	if len(spec.EnvAllowlist) == 0 {
		t.Fatal("默认应透传基础环境变量，否则来源进程跑不起来")
	}
	if spec.ExpectedProtocol.Format == "" || spec.ExpectedProtocol.Version == "" {
		t.Fatalf("expected_protocol 必须写明格式与版本: %+v", spec.ExpectedProtocol)
	}
	if errs := spec.Validate(); len(errs) > 0 {
		t.Fatalf("写出的规格必须自洽: %v", model.Err(errs))
	}
}

// 无显式来源时只写 run_meta：__runner 保持「驻留等信号」的现状。
func TestPrepareRunnerArtifactsWithoutSourceKeepsBehavior(t *testing.T) {
	f := newFixture(t)
	rt, err := NewRuntime(f.pc, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(managedLaunchSpecEnv, "")
	t.Setenv(managedLaunchCmdEnv, "")

	op := launchOp(f.pc)
	opDir := rt.OpDir(string(op.OperationID))
	if err := rt.PrepareRunnerArtifacts(opDir, op); err != nil {
		t.Fatalf("PrepareRunnerArtifacts: %v", err)
	}
	if meta := readRunMeta(t, opDir); meta.RunID == "" || meta.GlobalDB == "" {
		t.Fatalf("无 spec 时仍要写 run_meta: %+v", meta)
	}
	if _, err := os.Stat(filepath.Join(opDir, launchSpecFile)); !os.IsNotExist(err) {
		t.Fatalf("无来源时不得凭空造 launch_spec.json: %v", err)
	}
}

// 外部预置的规格优先：重复准备不得覆盖。
func TestPrepareRunnerArtifactsKeepsExistingSpec(t *testing.T) {
	f := newFixture(t)
	rt, err := NewRuntime(f.pc, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(managedLaunchSpecEnv, "")
	t.Setenv(managedLaunchCmdEnv, `["/usr/bin/env","echo","generated"]`)

	op := launchOp(f.pc)
	opDir := rt.OpDir(string(op.OperationID))
	if err := os.MkdirAll(opDir, 0o755); err != nil {
		t.Fatal(err)
	}
	preset := `{"executable":"/usr/bin/env","argv":["echo","preset"],"stdin_mode":"none","expected_protocol":{"format":"passthrough","version":"1"}}`
	specPath := filepath.Join(opDir, launchSpecFile)
	if err := os.WriteFile(specPath, []byte(preset), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rt.PrepareRunnerArtifacts(opDir, op); err != nil {
		t.Fatalf("PrepareRunnerArtifacts: %v", err)
	}
	spec := readLaunchSpec(t, opDir)
	if len(spec.Argv) != 2 || spec.Argv[1] != "preset" {
		t.Fatalf("不应覆盖已有规格: %v", spec.Argv)
	}
}

// 坏规格必须当场失败，不能写出 Runner 读不懂的文件。
func TestPrepareRunnerArtifactsRejectsInvalidSpec(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{name: "非 JSON", cmd: "not-json"},
		{name: "缺参数", cmd: `["/usr/bin/env"]`},
		{name: "拼 shell", cmd: `["/bin/sh","-c","echo hi"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			rt, err := NewRuntime(f.pc, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(managedLaunchSpecEnv, "")
			t.Setenv(managedLaunchCmdEnv, tc.cmd)

			op := launchOp(f.pc)
			opDir := rt.OpDir(string(op.OperationID))
			if err := rt.PrepareRunnerArtifacts(opDir, op); err == nil {
				t.Fatal("坏规格应直接失败")
			}
			if _, err := os.Stat(filepath.Join(opDir, launchSpecFile)); !os.IsNotExist(err) {
				t.Fatalf("坏规格不得落盘: %v", err)
			}
			// 元数据仍要写：运维排查要知道这次操作是谁。
			if meta := readRunMeta(t, opDir); meta.RunID == "" {
				t.Fatalf("run_meta 应已写好: %+v", meta)
			}
		})
	}
}

// 完整 LaunchSpec JSON 走同一个入口，显式字段不被默认值覆盖。
func TestPrepareRunnerArtifactsAcceptsFullSpec(t *testing.T) {
	f := newFixture(t)
	rt, err := NewRuntime(f.pc, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(managedLaunchCmdEnv, "")
	t.Setenv(managedLaunchSpecEnv, `{
		"executable":"/usr/bin/env",
		"argv":["echo","full"],
		"stdin_mode":"pipe",
		"env_allowlist":["PATH"],
		"expected_protocol":{"format":"stream-json","version":"2"}
	}`)

	op := launchOp(f.pc)
	opDir := rt.OpDir(string(op.OperationID))
	if err := rt.PrepareRunnerArtifacts(opDir, op); err != nil {
		t.Fatalf("PrepareRunnerArtifacts: %v", err)
	}
	spec := readLaunchSpec(t, opDir)
	if spec.StdinMode != model.StdinPipe {
		t.Fatalf("stdin_mode 被默认值覆盖: %q", spec.StdinMode)
	}
	if len(spec.EnvAllowlist) != 1 || spec.EnvAllowlist[0] != "PATH" {
		t.Fatalf("env_allowlist 被默认值覆盖: %v", spec.EnvAllowlist)
	}
	if spec.ExpectedProtocol.Format != "stream-json" || spec.ExpectedProtocol.Version != "2" {
		t.Fatalf("protocol 被默认值覆盖: %+v", spec.ExpectedProtocol)
	}
}

// 元数据兜底与 __runner 一致：缺字段也不会写出空白身份。
func TestRunMetaForFallsBackLikeRunner(t *testing.T) {
	f := newFixture(t)
	rt, err := NewRuntime(f.pc, "")
	if err != nil {
		t.Fatal(err)
	}
	meta := rt.RunMetaFor(dispatch.Operation{OperationID: model.OperationID("op_abc")})
	if meta.RunID != "run_abc" {
		t.Fatalf("run_id=%q want run_abc", meta.RunID)
	}
	if meta.InstanceID != "ti_systemagent" {
		t.Fatalf("instance_id=%q", meta.InstanceID)
	}
	if meta.Generation != 1 {
		t.Fatalf("generation=%d", meta.Generation)
	}
	if meta.ProjectID != f.pc.ID() || meta.GlobalDB != f.pc.GlobalDB {
		t.Fatalf("工程上下文未补全: %+v", meta)
	}
}
