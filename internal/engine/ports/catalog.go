package ports

import (
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// PortKind 是端口类别，与 ports_contract.py 的 kind 取值一致。
type PortKind string

// 用例／事务／协议／进程。
const (
	KindUseCase     PortKind = "use_case"
	KindTransaction PortKind = "transaction"
	KindProtocol    PortKind = "protocol"
	KindProcess     PortKind = "process"
)

// Valid 报告取值是否合法。
func (k PortKind) Valid() bool {
	return k == KindUseCase || k == KindTransaction || k == KindProtocol || k == KindProcess
}

// Descriptor 是一个端口的契约声明：名字、类别、输入、输出、可能错误。
// 它是「七个端口」这一说法在代码里唯一的落点；接线校验与文档对齐都查它。
type Descriptor struct {
	Name    string                `json:"port"`
	Kind    PortKind              `json:"kind"`
	Inputs  []string              `json:"inputs"`
	Outputs []string              `json:"outputs"`
	Errors  []model.PortErrorCode `json:"errors"`
}

// contract 是契约 §2 端口表的逐行副本，顺序与 ports_contract.PORTS 一致。
var contract = []Descriptor{
	{
		Name:    "ClaimExecution",
		Kind:    KindUseCase,
		Inputs:  []string{"project_id", "agent_id", "requested_task", "expected_revision", "idempotency_key", "capacity"},
		Outputs: []string{"task_instance", "task_version", "run_id", "generation", "leases", "operation_id"},
		Errors: []model.PortErrorCode{
			model.PortErrNoReady, model.PortErrConflict, model.PortErrRevisionMismatch,
			model.PortErrCapacityExhausted, model.PortErrIdempotentReplay,
		},
	},
	{
		Name:    "Store",
		Kind:    KindTransaction,
		Inputs:  []string{"ops", "expected_revision", "generation"},
		Outputs: []string{"cursor", "revision", "applied"},
		Errors: []model.PortErrorCode{
			model.PortErrBusy, model.PortErrConflict,
			model.PortErrInvalidTransition, model.PortErrUnknownIdentity,
		},
	},
	{
		Name:    "Probe",
		Kind:    KindProtocol,
		Inputs:  []string{"node_config"},
		Outputs: []string{"entry_state", "version", "readiness", "capabilities", "evidence_ref"},
		Errors: []model.PortErrorCode{
			model.PortErrEntryMissing, model.PortErrVersionUnparsed,
			model.PortErrProbeTimeout, model.PortErrPlatformUnsupported,
		},
	},
	{
		Name:    "Launch",
		Kind:    KindProtocol,
		Inputs:  []string{"run_spec"},
		Outputs: []string{"executable", "argv", "cwd", "stdin_mode", "env_allowlist", "expected_protocol"},
		Errors: []model.PortErrorCode{
			model.PortErrInvalidSpec, model.PortErrCapabilityMissing, model.PortErrEntryUnavailable,
		},
	},
	{
		Name:    "Decode",
		Kind:    KindProtocol,
		Inputs:  []string{"raw_record", "cursor", "protocol"},
		Outputs: []string{"events", "raw_ref", "quality"},
		Errors: []model.PortErrorCode{
			model.PortErrUnparseableRecord, model.PortErrUnknownRecordType, model.PortErrVersionUnsupported,
		},
	},
	{
		Name:    "Interpret",
		Kind:    KindProtocol,
		Inputs:  []string{"final_result", "application_error", "exit_code", "timeout", "artifacts"},
		Outputs: []string{"outcome", "result_text", "artifact_candidates", "quality"},
		Errors: []model.PortErrorCode{
			model.PortErrMissingResult, model.PortErrAuthRequired,
			model.PortErrApplication, model.PortErrProtocolUnknown,
		},
	},
	{
		Name:    "ProcessController",
		Kind:    KindProcess,
		Inputs:  []string{"launch_spec", "stop_request"},
		Outputs: []string{"process_handle", "stop_evidence"},
		Errors: []model.PortErrorCode{
			model.PortErrNotStarted, model.PortErrAlreadyStopped,
			model.PortErrStopUnconfirmed, model.PortErrPlatformUnsupported,
		},
	},
}

// Catalog 返回七个端口的契约声明（副本，调用方改不到内部表）。
func Catalog() []Descriptor {
	out := make([]Descriptor, len(contract))
	for i, d := range contract {
		out[i] = Descriptor{
			Name:    d.Name,
			Kind:    d.Kind,
			Inputs:  append([]string(nil), d.Inputs...),
			Outputs: append([]string(nil), d.Outputs...),
			Errors:  append([]model.PortErrorCode(nil), d.Errors...),
		}
	}
	return out
}

// PortNames 返回七个端口名，顺序即契约顺序。
func PortNames() []string {
	out := make([]string, 0, len(contract))
	for _, d := range contract {
		out = append(out, d.Name)
	}
	return out
}

// Lookup 按名字取端口声明。
func Lookup(name string) (Descriptor, bool) {
	for _, d := range contract {
		if strings.EqualFold(d.Name, name) {
			return d, true
		}
	}
	return Descriptor{}, false
}

// ValidateCatalog 自检端口表：七个端口齐、名字不重、类别合法、错误码已登记（P113）。
func ValidateCatalog() []*model.Error {
	var out []*model.Error
	if len(contract) != 7 {
		out = append(out, model.Violationf(model.CodePortUnbound, "catalog",
			"契约登记七个端口，实际 %d 个", len(contract)))
	}
	seen := make(map[string]bool, len(contract))
	for i, d := range contract {
		p := "catalog[" + d.Name + "]"
		if seen[d.Name] {
			out = append(out, model.Violationf(model.CodePortDuplicateBinding, p, "端口 %q 重复声明", d.Name))
		}
		seen[d.Name] = true
		if d.Name == "" {
			out = append(out, model.Violationf(model.CodePortUnknown, "catalog["+strconv.Itoa(i)+"]", "端口名不得为空"))
		}
		if !d.Kind.Valid() {
			out = append(out, model.Violationf(model.CodePortFieldInvalid, p+".kind", "未知端口类别 %q", d.Kind))
		}
		if len(d.Inputs) == 0 || len(d.Outputs) == 0 || len(d.Errors) == 0 {
			out = append(out, model.Violation(model.CodePortFieldInvalid, p,
				"输入、输出与可能错误都必须声明"))
		}
		for _, code := range d.Errors {
			if !code.Known() {
				out = append(out, model.Violationf(model.CodePortUnknown, p+".errors", "未登记的错误码 %q", code))
			}
		}
	}
	return out
}

// portForbiddenImports 是端口层绝不允许出现的依赖：
// 传输（HTTP）、存储（SQLite/sql）、进程创建（os/exec）与一切 maw 实现包。
// 判定口径与 ports_contract.py 的 P121 一致：禁的是**跨层**依赖（app／adapter／infra），
// 同层值对象（internal/engine/model）不在其中。
var portForbiddenImports = []string{
	"net/http",
	"net",
	"database/sql",
	"modernc.org/sqlite",
	"os/exec",
	"syscall",
	"github.com/kiwh77/maw/internal/db",
	"github.com/kiwh77/maw/internal/app",
	"github.com/kiwh77/maw/internal/gitops",
	"github.com/kiwh77/maw/internal/schedule",
	"github.com/kiwh77/maw/internal/machine",
	"github.com/kiwh77/maw/internal/adapters",
	"github.com/kiwh77/maw/internal/runner",
}

// ForbiddenForPort 报告某个 import 路径是否不允许出现在端口层。
// 规则的唯一落点在这里；自检与将来的接线校验都调用它，不各写一份。
func ForbiddenForPort(importPath string) bool {
	for _, bad := range portForbiddenImports {
		if importPath == bad || strings.HasPrefix(importPath, bad+"/") {
			return true
		}
	}
	return false
}

// AllowedInternalImport 报告某个 maw 内部 import 是否属于端口层允许的依赖。
// 只允许值对象层：端口可以引用契约定义，不能引用实现。
func AllowedInternalImport(importPath string) bool {
	const modulePrefix = "github.com/kiwh77/maw/"
	if !strings.HasPrefix(importPath, modulePrefix) {
		return false
	}
	return importPath == modulePrefix+"internal/engine/model"
}
