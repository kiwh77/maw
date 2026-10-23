package ports_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

func TestCatalogCoversSevenPortsInContractOrder(t *testing.T) {
	want := []string{"ClaimExecution", "Store", "Probe", "Launch", "Decode", "Interpret", "ProcessController"}
	if got := ports.PortNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("七个端口的名字或顺序漂移：\n want %v\n got  %v", want, got)
	}
	if vs := ports.ValidateCatalog(); len(vs) != 0 {
		t.Fatalf("端口表自检失败：%v", vs)
	}
}

func TestDescriptorDeclaresInputsOutputsAndErrors(t *testing.T) {
	for _, d := range ports.Catalog() {
		if !d.Kind.Valid() {
			t.Errorf("%s: 类别非法 %q", d.Name, d.Kind)
		}
		if len(d.Inputs) == 0 || len(d.Outputs) == 0 || len(d.Errors) == 0 {
			t.Errorf("%s: 输入/输出/错误必须都声明", d.Name)
		}
		for _, code := range d.Errors {
			if !code.Known() {
				t.Errorf("%s: 未登记的错误码 %q", d.Name, code)
			}
		}
	}
}

func TestClaimExecutionDeclaresItsFullErrorSet(t *testing.T) {
	d, ok := ports.Lookup("claimexecution") // 大小写不敏感
	if !ok {
		t.Fatal("按名字查不到 ClaimExecution")
	}
	want := []model.PortErrorCode{
		model.PortErrNoReady, model.PortErrConflict, model.PortErrRevisionMismatch,
		model.PortErrCapacityExhausted, model.PortErrIdempotentReplay,
	}
	if !reflect.DeepEqual(d.Errors, want) {
		t.Fatalf("ClaimExecution 错误集漂移：\n want %v\n got  %v", want, d.Errors)
	}
	// no_ready 与 conflict 必须分开：前者没题，后者被资源挡住。
	if model.PortErrNoReady.ClassOf() == model.PortErrConflict.ClassOf() {
		t.Fatal("no_ready 与 conflict 不得归为同一类")
	}
}

func TestCatalogIsCopy(t *testing.T) {
	first := ports.Catalog()
	first[0].Name = "Tampered"
	first[0].Errors[0] = model.PortErrBusy
	second := ports.Catalog()
	if second[0].Name != "ClaimExecution" {
		t.Fatal("Catalog 必须返回副本，调用方改不到内部表")
	}
	if second[0].Errors[0] != model.PortErrNoReady {
		t.Fatal("错误列表也必须深拷贝")
	}
}

func TestForbiddenDependencies(t *testing.T) {
	forbidden := []string{
		"net/http",
		"database/sql",
		"modernc.org/sqlite",
		"os/exec",
		"github.com/kiwh77/maw/internal/db",
		"github.com/kiwh77/maw/internal/adapters/codebuddy",
		"github.com/kiwh77/maw/internal/schedule",
	}
	for _, p := range forbidden {
		if !ports.ForbiddenForPort(p) {
			t.Errorf("%q 应被端口层禁止", p)
		}
	}
	allowed := []string{"context", "time", "strings", "github.com/kiwh77/maw/internal/engine/model"}
	for _, p := range allowed {
		if ports.ForbiddenForPort(p) {
			t.Errorf("%q 不应被禁止", p)
		}
	}
	if !ports.AllowedInternalImport("github.com/kiwh77/maw/internal/engine/model") {
		t.Fatal("model 是端口层唯一允许的内部依赖")
	}
	if ports.AllowedInternalImport("github.com/kiwh77/maw/internal/db") {
		t.Fatal("db 不得被端口层依赖")
	}
}

// stub 证明七个端口在不引入任何实现包的前提下可以被实现。
type stub struct{}

func (stub) Claim(ctx context.Context, req ports.ClaimRequest) (ports.ClaimResult, error) {
	return ports.ClaimResult{}, nil
}

func (stub) Apply(ctx context.Context, req ports.StoreRequest) (ports.StoreResult, error) {
	return ports.StoreResult{}, nil
}

func (stub) Probe(ctx context.Context, cfg model.NodeConfig) (model.ProbeResult, error) {
	return model.ProbeResult{}, nil
}

func (stub) BuildLaunchSpec(ctx context.Context, spec model.RunSpec) (model.LaunchSpec, error) {
	return model.LaunchSpec{}, nil
}

func (stub) Decode(ctx context.Context, req ports.DecodeRequest) (ports.DecodeResult, error) {
	return ports.DecodeResult{}, nil
}

func (stub) Interpret(ctx context.Context, req ports.InterpretRequest) (model.InterpretResult, error) {
	return model.InterpretResult{}, nil
}

func (stub) Start(ctx context.Context, spec model.LaunchSpec, runID model.RunID, gen model.Generation) (model.ProcessHandle, error) {
	return model.ProcessHandle{}, nil
}

func (stub) Stop(ctx context.Context, req model.StopRequest) (model.StopEvidence, error) {
	return model.StopEvidence{}, nil
}

var (
	_ ports.ClaimExecution    = stub{}
	_ ports.Store             = stub{}
	_ ports.Probe             = stub{}
	_ ports.Launch            = stub{}
	_ ports.Decode            = stub{}
	_ ports.Interpret         = stub{}
	_ ports.ProcessController = stub{}
)

func TestClaimRequestGuards(t *testing.T) {
	req := ports.ClaimRequest{
		ProjectID:      "716c30878ba1b95f",
		AgentID:        "codebuddy-goalagents-2",
		IdempotencyKey: "claim-1",
	}
	if vs := req.Validate(); len(vs) != 0 {
		t.Fatalf("完整领取请求应合法：%v", vs)
	}

	noKey := req
	noKey.IdempotencyKey = ""
	if !model.HasCode(noKey.Validate(), model.CodeClaimWithoutGuards) {
		t.Fatal("缺幂等键必须触发 P134")
	}

	badTask := req
	badTask.RequestedTask = taskRef("VN-201")
	if vs := badTask.Validate(); len(vs) == 0 {
		t.Fatal("指定任务写成显示编号应被拒")
	}
}

func taskRef(v string) *model.InstanceID {
	id := model.InstanceID(v)
	return &id
}

func TestStoreRequestGuards(t *testing.T) {
	rev := model.Revision(3)
	good := ports.StoreRequest{Ops: []ports.StoreOp{{
		Kind:             ports.StoreTaskStatus,
		ExpectedRevision: &rev,
		TaskStatus: &ports.TaskStatusWrite{
			Instance: "ti_vn201a",
			Status:   model.TaskInProgress,
		},
	}}}
	if vs := good.Validate(); len(vs) != 0 {
		t.Fatalf("带预期 revision 的状态写入应合法：%v", vs)
	}

	unconditional := ports.StoreRequest{Ops: []ports.StoreOp{{
		Kind:       ports.StoreTaskStatus,
		TaskStatus: &ports.TaskStatusWrite{Instance: "ti_vn201a", Status: model.TaskInProgress},
	}}}
	if vs := unconditional.Validate(); len(vs) == 0 {
		t.Fatal("无 expected_revision / generation 的写入必须被拒")
	}

	twoPayloads := ports.StoreRequest{Ops: []ports.StoreOp{{
		Kind:             ports.StoreRunPhase,
		ExpectedRevision: &rev,
		TaskStatus:       &ports.TaskStatusWrite{Instance: "ti_vn201a", Status: model.TaskInProgress},
		RunPhase:         &ports.RunPhaseWrite{RunID: "run_a001", Phase: model.PhaseRunning},
	}}}
	if vs := twoPayloads.Validate(); len(vs) == 0 {
		t.Fatal("一个操作带两个载荷必须被拒")
	}

	empty := ports.StoreRequest{}
	if vs := empty.Validate(); len(vs) == 0 {
		t.Fatal("空事务必须被拒")
	}
}

func TestDecodeAndInterpretGuards(t *testing.T) {
	dec := ports.DecodeRequest{RecordRef: "", Cursor: 0, Protocol: model.Protocol{}}
	if vs := dec.Validate(); len(vs) < 3 {
		t.Fatalf("无位置、无游标、无协议应各报一条：%v", vs)
	}
	if vs := (ports.InterpretRequest{}).Validate(); len(vs) == 0 {
		t.Fatal("没有任何可判定依据的请求必须被拒")
	}
	exit := 0
	if vs := (ports.InterpretRequest{ExitCode: &exit}).Validate(); len(vs) != 0 {
		t.Fatalf("只有退出码也是合法输入（判定不是成功由结果层负责）：%v", vs)
	}
}
