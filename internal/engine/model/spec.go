package model

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// AdapterKind 是接入器种类。
type AdapterKind string

// 四个已登记来源。
const (
	AdapterCodeBuddy AdapterKind = "codebuddy"
	AdapterWorkBuddy AdapterKind = "workbuddy"
	AdapterTrae      AdapterKind = "trae"
	AdapterZCode     AdapterKind = "zcode"
	AdapterLocalCLI  AdapterKind = "local_cli"
)

// Valid 报告取值是否合法。
func (a AdapterKind) Valid() bool {
	switch a {
	case AdapterCodeBuddy, AdapterWorkBuddy, AdapterTrae, AdapterZCode, AdapterLocalCLI:
		return true
	}
	return false
}

// InputMode 是任务输入的投递方式；不拼 shell 命令。
type InputMode string

// 标准输入／受控文件／参数。
const (
	InputStdin InputMode = "stdin"
	InputFile  InputMode = "file"
	InputArg   InputMode = "arg"
)

// Valid 报告取值是否合法。
func (m InputMode) Valid() bool { return m == InputStdin || m == InputFile || m == InputArg }

// StdinMode 是启动规格里的标准输入模式。
type StdinMode string

// 不接／管道／文件。
const (
	StdinNone StdinMode = "none"
	StdinPipe StdinMode = "pipe"
	StdinFile StdinMode = "file"
)

// Valid 报告取值是否合法。
func (m StdinMode) Valid() bool { return m == StdinNone || m == StdinPipe || m == StdinFile }

// WorkspaceSpec 是运行目录与基线。
type WorkspaceSpec struct {
	// Path 必须是绝对路径：相对路径会让「同一个目录」随调用者工作目录漂移。
	Path           string `json:"path"`
	BaselineCommit Commit `json:"baseline_commit"`
	Branch         string `json:"branch,omitempty"`
}

// PermissionProfile 是执行权限档引用。
type PermissionProfile struct {
	Profile      string `json:"profile"`
	AllowNetwork bool   `json:"allow_network"`
	AllowWrite   bool   `json:"allow_write"`
	Sandbox      string `json:"sandbox,omitempty"`
}

// SessionSpec 是会话与 MCP 配置。恢复会话必须显式给 session id，不选「最近一次」。
type SessionSpec struct {
	Isolation       string   `json:"isolation,omitempty"`
	ResumeSessionID string   `json:"resume_session_id,omitempty"`
	MCPServers      []string `json:"mcp_servers,omitempty"`
}

// RunSpec 是冻结的执行规格：一次 Run 启动前确定，之后不再改。
type RunSpec struct {
	Instance   InstanceID        `json:"task_instance"`
	Version    VersionNo         `json:"task_version"`
	RunID      RunID             `json:"run_id"`
	Generation Generation        `json:"generation"`
	Workspace  WorkspaceSpec     `json:"workspace"`
	Input      string            `json:"input"`
	InputMode  InputMode         `json:"input_mode"`
	Permission PermissionProfile `json:"permission"`
	Session    SessionSpec       `json:"session"`
	Timeout    time.Duration     `json:"timeout"`
}

// Validate 拒绝不完整或不可判定的运行规格。
func (s RunSpec) Validate() []*Error {
	var out []*Error
	if !s.Instance.Valid() {
		out = append(out, Violation(CodePortFieldInvalid, "run_spec.task_instance", "必须绑定任务实例"))
	}
	if !s.Version.Valid() {
		out = append(out, Violation(CodePortFieldInvalid, "run_spec.task_version", "必须绑定任务定义版本"))
	}
	if !s.RunID.Valid() {
		out = append(out, Violation(CodePortFieldInvalid, "run_spec.run_id", "必须绑定运行身份"))
	}
	if !s.Generation.Valid() {
		out = append(out, Violation(CodePortFieldInvalid, "run_spec.generation", "代际必须从 1 开始"))
	}
	if !isAbsPath(s.Workspace.Path) {
		out = append(out, Violation(CodePortFieldInvalid, "run_spec.workspace.path", "必须是绝对路径"))
	}
	if !s.Workspace.BaselineCommit.Valid() {
		out = append(out, Violation(CodeCommitRefInvalid, "run_spec.workspace.baseline_commit", "必须记录基线提交"))
	}
	if !s.InputMode.Valid() {
		out = append(out, Violationf(CodePortFieldInvalid, "run_spec.input_mode",
			"只能是 stdin/file/arg，收到 %q", s.InputMode))
	}
	if strings.TrimSpace(s.Input) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "run_spec.input", "任务输入不得为空"))
	}
	if strings.TrimSpace(s.Permission.Profile) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "run_spec.permission.profile", "必须给权限档引用"))
	}
	if s.Timeout <= 0 {
		out = append(out, Violation(CodePortFieldInvalid, "run_spec.timeout", "超时必须为正"))
	}
	if s.Session.ResumeSessionID != "" && strings.TrimSpace(s.Session.Isolation) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "run_spec.session.isolation",
			"恢复会话必须声明隔离条件，不得靠「最近一次会话」"))
	}
	return out
}

// Protocol 是预期输出协议，必须写死格式与版本，不许「看情况」。
type Protocol struct {
	Format  string `json:"format"`
	Version string `json:"version"`
}

// LaunchSpec 是进程启动规格：exe + argv 数组，绝不拼 shell。
type LaunchSpec struct {
	Executable       string    `json:"executable"`
	Argv             []string  `json:"argv"`
	Cwd              string    `json:"cwd"`
	StdinMode        StdinMode `json:"stdin_mode"`
	EnvAllowlist     []string  `json:"env_allowlist"`
	ExpectedProtocol Protocol  `json:"expected_protocol"`
}

var (
	envNameRE  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	shellNames = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "csh": true, "fish": true}
)

// Validate 拒绝拼 shell、黑名单式环境变量与含糊协议。
func (s LaunchSpec) Validate() []*Error {
	var out []*Error
	if !isAbsPath(s.Executable) {
		out = append(out, Violation(CodePortFieldInvalid, "launch_spec.executable", "必须是绝对路径"))
	}
	if base := filepath.Base(strings.TrimSpace(s.Executable)); shellNames[base] {
		out = append(out, Violation(CodePortFieldInvalid, "launch_spec.executable",
			"禁止拼 shell：必须直接指向来源可执行文件，不得借 sh/bash 解释命令行"))
	}
	if len(s.Argv) == 0 {
		out = append(out, Violation(CodePortFieldInvalid, "launch_spec.argv", "argv 必须是非空数组，不得用一条 shell 字符串代替"))
	}
	for i, a := range s.Argv {
		if strings.ContainsAny(a, "\x00\n\r") {
			out = append(out, Violationf(CodePortFieldInvalid, "launch_spec.argv",
				"第 %d 个参数含 NUL 或换行，无法作为单个 argv 元素传递", i))
		}
	}
	if s.Cwd != "" && !isAbsPath(s.Cwd) {
		out = append(out, Violation(CodePortFieldInvalid, "launch_spec.cwd", "必须是绝对路径"))
	}
	if !s.StdinMode.Valid() {
		out = append(out, Violationf(CodePortFieldInvalid, "launch_spec.stdin_mode", "未知标准输入模式 %q", s.StdinMode))
	}
	for _, name := range s.EnvAllowlist {
		if !envNameRE.MatchString(name) {
			out = append(out, Violationf(CodePortFieldInvalid, "launch_spec.env_allowlist",
				"%q 不是合法变量名：白名单只写变量名，不写 KEY=VALUE，也不接受通配符", name))
		}
	}
	if strings.TrimSpace(s.ExpectedProtocol.Format) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "launch_spec.expected_protocol.format", "必须写明预期输出格式"))
	}
	if strings.TrimSpace(s.ExpectedProtocol.Version) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "launch_spec.expected_protocol.version", "必须写明预期协议版本"))
	}
	return out
}

// NodeConfig 是节点配置：身份、入口、配置档、模式与槽数。
type NodeConfig struct {
	NodeID      NodeID        `json:"node_id"`
	Name        string        `json:"name"`
	Adapter     AdapterKind   `json:"adapter_kind"`
	EntryPath   string        `json:"entry_path"`
	RuntimePath string        `json:"runtime_path"`
	ProfileRef  string        `json:"profile_ref"`
	Mode        NodeMode      `json:"mode"`
	Platform    string        `json:"platform"`
	SlotLimit   int           `json:"slot_limit"`
	AcceptNew   bool          `json:"accept_new"`
	Timeout     time.Duration `json:"timeout"`
}

// Validate 拒绝不完整节点配置；受管模式必须给出正整数槽数。
func (c NodeConfig) Validate() []*Error {
	var out []*Error
	if !c.NodeID.Valid() {
		out = append(out, Violation(CodePortFieldInvalid, "node.node_id", "必须是小写字母开头的节点标识"))
	}
	if strings.TrimSpace(c.Name) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "node.name", "必须给展示名"))
	}
	if !c.Adapter.Valid() {
		out = append(out, Violationf(CodePortFieldInvalid, "node.adapter_kind", "未知接入器 %q", c.Adapter))
	}
	if !isAbsPath(c.EntryPath) {
		out = append(out, Violation(CodePortFieldInvalid, "node.entry_path", "必须是绝对路径"))
	}
	if c.RuntimePath != "" && !isAbsPath(c.RuntimePath) {
		out = append(out, Violation(CodePortFieldInvalid, "node.runtime_path", "必须是绝对路径"))
	}
	if strings.TrimSpace(c.ProfileRef) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "node.profile_ref", "必须给配置档引用"))
	}
	if !c.Mode.Valid() {
		out = append(out, Violationf(CodePortFieldInvalid, "node.mode", "未知接入模式 %q", c.Mode))
	}
	if strings.TrimSpace(c.Platform) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "node.platform", "必须写明平台"))
	}
	if c.Mode == NodeManagedCLI && c.SlotLimit < 1 {
		out = append(out, Violation(CodePortFieldInvalid, "node.slot_limit", "受管节点至少一个槽"))
	}
	if c.Timeout < 0 {
		out = append(out, Violation(CodePortFieldInvalid, "node.timeout", "超时不得为负"))
	}
	return out
}

// Capability 是一项能力声明／验证值；能力必须绑定版本与证据（P140）。
type Capability struct {
	Name        string          `json:"name"`
	State       CapabilityState `json:"state"`
	Version     SemVer          `json:"capability_version"`
	EvidenceRef string          `json:"evidence"`
}

// Validate 拒绝无版本或无证据的能力绑定。
func (c Capability) Validate(path string) []*Error {
	var out []*Error
	if strings.TrimSpace(c.Name) == "" {
		out = append(out, Violation(CodePortFieldInvalid, path+".name", "必须给能力名"))
	}
	if !c.State.Valid() {
		out = append(out, Violationf(CodePortFieldInvalid, path+".state", "未知能力状态 %q", c.State))
	}
	if !c.Version.Valid() {
		out = append(out, Violationf(CodeCapabilityUnversioned, path+".capability_version",
			"必须是 x.y.z 语义化版本，收到 %q", c.Version))
	}
	if strings.TrimSpace(c.EvidenceRef) == "" {
		out = append(out, Violation(CodeCapabilityUnversioned, path+".evidence", "必须挂证据目录引用"))
	}
	return out
}

// Readiness 是节点四个维度的就绪情况，不塞进一个 online 字段。
type Readiness struct {
	Entry      EntryState     `json:"entry"`
	Auth       ReadinessState `json:"auth"`
	Connection ReadinessState `json:"connection"`
	Capacity   ReadinessState `json:"capacity"`
}

// Ready 报告入口与认证都已就绪且容量可接新任务。
func (r Readiness) Ready() bool {
	return r.Entry == EntryReady && r.Auth == ReadyOK && r.Capacity.AcceptsNewWork()
}

// ProbeResult 是探测结果；只读，不登录、不发模型任务。
type ProbeResult struct {
	EntryState   EntryState   `json:"entry_state"`
	Version      string       `json:"version"`
	Readiness    Readiness    `json:"readiness"`
	Capabilities []Capability `json:"capabilities"`
	EvidenceRef  string       `json:"evidence_ref"`
}

// Validate 拒绝无版本、能力缺版本或证据的探测结果。
func (p ProbeResult) Validate() []*Error {
	var out []*Error
	if !p.EntryState.Valid() {
		out = append(out, Violationf(CodePortFieldInvalid, "probe.entry_state", "未知入口状态 %q", p.EntryState))
	}
	if strings.TrimSpace(p.Version) == "" {
		out = append(out, Violation(CodeCapabilityUnversioned, "probe.version", "探测结果必须带版本"))
	}
	if strings.TrimSpace(p.EvidenceRef) == "" {
		out = append(out, Violation(CodeCapabilityUnversioned, "probe.evidence_ref", "必须挂证据引用"))
	}
	for i, c := range p.Capabilities {
		out = append(out, c.Validate(fmt.Sprintf("probe.capabilities[%d]", i))...)
	}
	for name, st := range map[string]ReadinessState{
		"auth": p.Readiness.Auth, "connection": p.Readiness.Connection, "capacity": p.Readiness.Capacity,
	} {
		if !st.Valid() {
			out = append(out, Violationf(CodePortFieldInvalid, "probe.readiness."+name, "未知就绪取值 %q", st))
		}
	}
	return out
}

// Outcome 是运行结果判定：completed 只说明协议走完并产出可判定结果，不代表验收通过。
type Outcome string

// 五种运行结果。
const (
	OutcomeCompleted Outcome = "completed"
	OutcomeFailed    Outcome = "failed"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeTimedOut  Outcome = "timed_out"
	OutcomeUnknown   Outcome = "unknown"
)

// Valid 报告取值是否合法。
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeCompleted, OutcomeFailed, OutcomeCancelled, OutcomeTimedOut, OutcomeUnknown:
		return true
	}
	return false
}

// IsSuccess 只对 completed 为真；unknown 不得降级为成功（P142）。
func (o Outcome) IsSuccess() bool { return o == OutcomeCompleted }

// ArtifactCandidate 是接入器声明的候选产物；未核实前不算产物。
type ArtifactCandidate struct {
	Kind       ArtifactKind `json:"kind"`
	Ref        string       `json:"ref"`
	DeclaredBy Declarer     `json:"declared_by"`
	SHA256     *SHA256      `json:"sha256,omitempty"`
}

// InterpretResult 是联合结构化结果、应用错误、退出码与超时后的判定。
type InterpretResult struct {
	Outcome    Outcome             `json:"outcome"`
	ResultText string              `json:"result_text,omitempty"`
	Artifacts  []ArtifactCandidate `json:"artifact_candidates,omitempty"`
	Quality    Quality             `json:"quality"`
}

// Validate 拒绝未知结果与非法候选产物。
func (r InterpretResult) Validate() []*Error {
	var out []*Error
	if !r.Outcome.Valid() {
		out = append(out, Violationf(CodePortFieldInvalid, "interpret.outcome", "未知运行结果 %q", r.Outcome))
	}
	if !r.Quality.Valid() {
		out = append(out, Violationf(CodePortFieldInvalid, "interpret.quality", "未知证据质量 %q", r.Quality))
	}
	for i, a := range r.Artifacts {
		p := fmt.Sprintf("interpret.artifact_candidates[%d]", i)
		if !a.Kind.Valid() {
			out = append(out, Violationf(CodePortFieldInvalid, p+".kind", "未知产物类型 %q", a.Kind))
		}
		if strings.TrimSpace(a.Ref) == "" {
			out = append(out, Violation(CodePortFieldInvalid, p+".ref", "候选产物必须带引用"))
		}
		if !a.DeclaredBy.Valid() {
			out = append(out, Violationf(CodePortFieldInvalid, p+".declared_by", "只能是 cli 或 manual，收到 %q", a.DeclaredBy))
		}
	}
	return out
}

// StopMode 是停止方式。
type StopMode string

// 优雅停止／强制停止。
const (
	StopGraceful StopMode = "graceful"
	StopForce    StopMode = "force"
)

// Valid 报告取值是否合法。
func (m StopMode) Valid() bool { return m == StopGraceful || m == StopForce }

// StopRequest 是停止请求：必须带代际，旧代际不得停新代际。
type StopRequest struct {
	RunID      RunID      `json:"run_id"`
	Generation Generation `json:"generation"`
	Mode       StopMode   `json:"mode"`
}

// Validate 拒绝不完整停止请求。
func (r StopRequest) Validate() []*Error {
	var out []*Error
	if !r.RunID.Valid() {
		out = append(out, Violation(CodePortFieldInvalid, "stop.run_id", "必须绑定运行身份"))
	}
	if !r.Generation.Valid() {
		out = append(out, Violation(CodePortFieldInvalid, "stop.generation", "代际必须从 1 开始"))
	}
	if !r.Mode.Valid() {
		out = append(out, Violationf(CodePortFieldInvalid, "stop.mode", "未知停止方式 %q", r.Mode))
	}
	return out
}

// ProcessHandle 是进程身份：只有 pid 不足以判定归属，必须带启动令牌。
type ProcessHandle struct {
	PID        int    `json:"pid"`
	PGID       int    `json:"pgid"`
	StartToken string `json:"start_token"`
}

// Validate 拒绝缺启动令牌的进程身份。
func (h ProcessHandle) Validate() []*Error {
	var out []*Error
	if h.PID <= 0 {
		out = append(out, Violation(CodePortFieldInvalid, "process.pid", "必须为正整数"))
	}
	if strings.TrimSpace(h.StartToken) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "process.start_token", "必须带启动身份，不能只凭 PID 判归属"))
	}
	return out
}

// StopEvidence 是停止证据。stop_confirmed=false 是合法结果，不是失败。
type StopEvidence struct {
	StopConfirmed bool   `json:"stop_confirmed"`
	Method        string `json:"method"`
	EvidenceRef   string `json:"evidence_ref"`
}

// Validate 拒绝无方法的停止结论；「已确认停止」必须有可追溯证据，
// 未确认（stop_unconfirmed）是合法结果，只要求写清手段。
func (e StopEvidence) Validate() []*Error {
	var out []*Error
	if strings.TrimSpace(e.Method) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "stop_evidence.method", "必须写明停止手段"))
	}
	if e.StopConfirmed && strings.TrimSpace(e.EvidenceRef) == "" {
		out = append(out, Violation(CodePortFieldInvalid, "stop_evidence.evidence_ref", "确认停止必须可追溯"))
	}
	return out
}

func isAbsPath(p string) bool {
	p = strings.TrimSpace(p)
	return p != "" && filepath.IsAbs(p)
}
