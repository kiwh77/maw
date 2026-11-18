package systemagent

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/dbstore"
	"github.com/kiwh77/maw/internal/runner/observe"
)

// 本文件是系统 Agent 诊断的 Pi 后端（契约 maw.pi.execution/v1 §3 `diagnose` 模式）。
//
// 与旧的 ExecInvoker 的差别：
//   - 同一个 Pi 运行时也执行 Worker／Lead，诊断只是换成只读模式，不另起一套推理栈；
//   - 决策原文仍由编排器交给 ParseDecision 与既有动作策略，这里只负责「跑一次、
//     收事实、留证据」；
//   - 每次诊断都有作业身份（run_id 由 job 派生），所以用量能归到「诊断 + 对应作业」；
//   - 超时／认证失败／非法结构都不静默：落 journal、写审计摘要、返回带类别的错误。

const (
	// diagnoseRunPrefix 是诊断运行的 run_id 前缀：job → run 可反查。
	diagnoseRunPrefix = "run_diag_"
	// diagnoseJournalFile 是运行时事件流的逐行原文（0600）。
	diagnoseJournalFile = "pi-diagnose.ndjson"
	// diagnoseAuditFile 是一次诊断的可读结论（0600）。
	diagnoseAuditFile = "pi-diagnose.json"
	// diagnoseDefaultTimeout 是未配置超时时的预算（与旧 llm_command 的 3 分钟一致）。
	diagnoseDefaultTimeout = 3 * time.Minute
)

// piDiagnoseContract 是诊断事件流必须携带的契约标识。
const piDiagnoseContract = "maw.pi.execution/v1"

// piDiagnoseEnv 是诊断运行时允许继承的宿主环境变量名。
//
// 模型凭据只走白名单（契约 §6）：工具子进程的环境由运行时自己剔除密钥前缀。
var piDiagnoseEnv = []string{
	"HOME", "PATH", "LANG", "SHELL", "TMPDIR",
	"PI_MODEL", "PI_PROVIDER", "PI_BASE_URL", "PI_API_KEY",
}

// PiDiagnoseError 是一诊断失败的可审计结论。
//
// Category 取契约 §7.2 的封闭集合；Journal 指向本次事件流原文，
// 即使作业被重排，也能从审计目录查到「当时到底发生了什么」。
type PiDiagnoseError struct {
	Category string
	Message  string
	Journal  string
	ExitCode int
}

func (e *PiDiagnoseError) Error() string {
	if e == nil {
		return "systemagent: Pi 诊断失败"
	}
	parts := []string{"Pi 诊断失败（category=" + e.Category + "）: " + strings.TrimSpace(e.Message)}
	if e.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit_code=%d", e.ExitCode))
	}
	if e.Journal != "" {
		parts = append(parts, "journal="+e.Journal)
	}
	return strings.Join(parts, "；")
}

// piRunResult 是一次运行时进程的原始输出。
type piRunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// piExecFunc 执行运行时进程；测试可注入替身（不依赖 node）。
type piExecFunc func(ctx context.Context, argv []string, cwd string, env []string, stdin string) (piRunResult, error)

// PiInvoker 用 Pi 运行时的 diagnose 模式产出决策原文。
//
// 只读：write_scope 恒为空、allow_bash 恒为假（契约 §3／§6）。
type PiInvoker struct {
	// NodePath 是 node 可执行文件；空时按 "node" 解析。
	NodePath string
	// RuntimePath 是运行时入口（runtimes/pi/src/main.mjs）的绝对路径。
	RuntimePath string
	// Cwd 是诊断进程的工作目录（工程根）。
	Cwd string
	// Model 是可选模型覆盖（节点 meta.default_model）。
	Model string
	// NodeID 是本次诊断使用的节点；写进用量归属。
	NodeID string
	// Timeout 是单次诊断预算；非正值用默认 3 分钟。
	Timeout time.Duration
	// Store 是状态库；为空时只产决策、不落用量（测试与只读场景）。
	Store *db.Store
	// Clock 是审计时间源；为空用系统时间。
	Clock func() time.Time
	// Exec 是进程执行器；为空走 exec.CommandContext。
	Exec piExecFunc
}

// piDiagnoseConfig 是写进运行时 stdin 的诊断配置（runtimes/pi 的约定）。
type piDiagnoseConfig struct {
	Mode       string   `json:"mode"`
	Cwd        string   `json:"cwd"`
	RunID      string   `json:"run_id"`
	Generation int      `json:"generation"`
	WriteScope []string `json:"write_scope"`
	AllowBash  bool     `json:"allow_bash"`
	Model      string   `json:"model,omitempty"`
	Prompt     string   `json:"prompt"`
}

// piEventEnvelope 是运行时事件信封（契约 §5.2）。
type piEventEnvelope struct {
	Contract   string          `json:"contract"`
	EventID    string          `json:"event_id"`
	Type       string          `json:"type"`
	RunID      string          `json:"run_id"`
	Generation int             `json:"generation"`
	CallID     string          `json:"call_id"`
	OccurredAt string          `json:"occurred_at"`
	ReceivedAt string          `json:"received_at"`
	Payload    json.RawMessage `json:"payload"`
}

// piResultPayload 是唯一终态（契约 §7.1）。
type piResultPayload struct {
	OK       bool     `json:"ok"`
	Outcome  string   `json:"outcome"`
	Reason   string   `json:"reason"`
	Text     string   `json:"text"`
	Evidence []string `json:"evidence"`
}

// piErrorPayload 是失败证据（契约 §7.2）。
type piErrorPayload struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

// piVersionPayload 是启动自述（契约 §2）：协议与模式都要与请求一致。
type piVersionPayload struct {
	SDKVersion string `json:"sdk_version"`
	Mode       string `json:"mode"`
	Protocol   struct {
		Format  string `json:"format"`
		Version string `json:"version"`
	} `json:"protocol"`
}

// piDiagnoseStream 是一次事件流的解析结果。
type piDiagnoseStream struct {
	Text          string
	OK            bool
	ResultSeen    bool
	Reason        string
	Evidence      []string
	ErrorCategory string
	ErrorMessage  string
	SDKVersion    string
	ProtocolOK    bool
	ModeOK        bool
	UnknownTypes  int
	Malformed     int
	Usage         []observe.Facts
	// Invalid 是结构层面的问题（契约不符／模式不符／缺终态），非空即不可采信。
	Invalid []string
}

// Diagnose 实现 Invoker：跑一次 diagnose 模式的 Pi 运行时，返回决策原文。
//
// 顺序：解析入口 → 起进程（带超时）→ 落 journal → 解析事件 → 归集用量 → 审计摘要。
// 任何一步失败都返回 PiDiagnoseError，且此前已经产生的证据都保留。
func (p *PiInvoker) Diagnose(ctx context.Context, req DiagnoseRequest) (string, error) {
	if p == nil {
		return "", &PiDiagnoseError{Category: "env", Message: "PiInvoker 为 nil"}
	}
	runtimePath := strings.TrimSpace(p.RuntimePath)
	if runtimePath == "" {
		return "", &PiDiagnoseError{Category: "env", Message: "未配置 Pi 运行时入口（runtimes/pi/src/main.mjs）"}
	}
	if !filepath.IsAbs(runtimePath) {
		// 相对路径会随调用者目录漂移：同一个节点不该有第二个入口含义。
		return "", &PiDiagnoseError{Category: "env", Message: "Pi 运行时入口必须是绝对路径，收到 " + runtimePath}
	}
	nodePath := strings.TrimSpace(p.NodePath)
	if nodePath == "" {
		nodePath = "node"
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = diagnoseDefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	runID := diagnoseRunID(req)
	cwd := strings.TrimSpace(p.Cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(req.ProjectPath)
	}
	if cwd == "" || !filepath.IsAbs(cwd) {
		// 运行时要求 cwd 是绝对路径：缺了就拒绝启动，不让它跑到当前目录。
		return "", &PiDiagnoseError{Category: "env", Message: "诊断缺少绝对工作目录（cwd=" + cwd + "）"}
	}
	cfg := piDiagnoseConfig{
		Mode:       "diagnose",
		Cwd:        cwd,
		RunID:      runID,
		Generation: 1,
		WriteScope: []string{}, // 诊断没有写权限：空集合是授权事实，不是省略
		AllowBash:  false,
		Model:      strings.TrimSpace(p.Model),
		Prompt:     BuildDiagnosePrompt(req),
	}
	stdin, err := json.Marshal(cfg)
	if err != nil {
		return "", &PiDiagnoseError{Category: "env", Message: "无法编码诊断配置: " + err.Error()}
	}

	exec := p.Exec
	if exec == nil {
		exec = defaultPiExec
	}
	res, runErr := exec(cctx, []string{nodePath, runtimePath}, cwd, piDiagnoseEnvValues(), string(stdin))

	jobDir := p.jobAuditDir(req)
	journal := writePiJournal(jobDir, res)

	stream := parsePiDiagnoseStream(res.Stdout)
	written, usageErr := p.recordUsage(ctx, req, runID, stream.Usage)
	p.linkJobRun(req, runID, jobDir)
	p.writeAuditSummary(jobDir, req, runID, res, stream, written, usageErr, journal)

	switch {
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		return "", &PiDiagnoseError{
			Category: "timeout",
			Message:  fmt.Sprintf("诊断超时（%s，stderr 摘要: %s）", timeout, truncate(strings.TrimSpace(res.Stderr), 300)),
			Journal:  journal, ExitCode: res.ExitCode,
		}
	case ctx.Err() != nil:
		return "", &PiDiagnoseError{
			Category: "unknown",
			Message:  "诊断被取消: " + ctx.Err().Error(),
			Journal:  journal, ExitCode: res.ExitCode,
		}
	}
	if runErr != nil {
		// 进程起不来是环境类失败（契约 §7.2 `env`），与模型判断失败分开。
		return "", &PiDiagnoseError{
			Category: "env",
			Message:  "无法启动 Pi 运行时 " + nodePath + " " + runtimePath + ": " + runErr.Error(),
			Journal:  journal, ExitCode: res.ExitCode,
		}
	}
	if len(stream.Invalid) > 0 {
		return "", &PiDiagnoseError{
			Category: "invalid_output",
			Message:  strings.Join(stream.Invalid, "；"),
			Journal:  journal, ExitCode: res.ExitCode,
		}
	}
	if !stream.ResultSeen {
		// 没有终态就不能判成功：退出码 0 单独不构成完成（契约 P160）。
		// exit 2 是运行时的「配置被拒绝」（环境类）；其它非零退出按崩溃记，
		// 两者都不与「模型输出非法」混在一起。
		category := "invalid_output"
		msg := fmt.Sprintf("事件流没有 result 终态（exit_code=%d，stderr 摘要: %s）",
			res.ExitCode, truncate(strings.TrimSpace(res.Stderr), 300))
		switch {
		case res.ExitCode == 2:
			category = "env"
			msg = "Pi 运行时拒绝诊断配置（exit 2）: " + truncate(strings.TrimSpace(res.Stderr), 300)
		case res.ExitCode != 0:
			category = "crash"
			msg = fmt.Sprintf("运行时异常退出且没有 result 终态（exit_code=%d，stderr 摘要: %s）",
				res.ExitCode, truncate(strings.TrimSpace(res.Stderr), 300))
		}
		return "", &PiDiagnoseError{Category: category, Message: msg, Journal: journal, ExitCode: res.ExitCode}
	}
	if !stream.OK {
		category := diagnoseCategory(stream.ErrorCategory)
		if category == "" || category == "unknown" {
			if c := diagnoseCategory(stream.Reason); c != "" {
				category = c
			}
		}
		if category == "" {
			category = "unknown"
		}
		msg := firstNonEmptyString(stream.ErrorMessage, stream.Reason, "运行时报告 ok=false")
		return "", &PiDiagnoseError{
			Category: category, Message: msg,
			Journal: journal, ExitCode: res.ExitCode,
		}
	}
	text := strings.TrimSpace(stream.Text)
	if text == "" {
		return "", &PiDiagnoseError{
			Category: "invalid_output",
			Message:  "result.ok=true 但 text 为空：没有可解析的决策原文",
			Journal:  journal, ExitCode: res.ExitCode,
		}
	}
	return text, nil
}

func (p *PiInvoker) now() time.Time {
	if p != nil && p.Clock != nil {
		return p.Clock().UTC()
	}
	return time.Now().UTC()
}

// diagnoseRunID 由作业身份派生诊断运行标识。
//
// 同一作业重跑仍是同一个 run：用量按 (source_ns, turn_id, epoch, scope) 去重，
// 而 call_id 由运行时每次新生成，所以重跑不会冒充同一次调用（契约 P131／P180）。
func diagnoseRunID(req DiagnoseRequest) string {
	key := strings.TrimSpace(req.JobID)
	if key == "" {
		key = strings.TrimSpace(req.TaskID)
	}
	if key == "" {
		key = "adhoc"
	}
	return diagnoseRunPrefix + sanitize(key)
}

// jobAuditDir 返回本次诊断的审计目录：<state>/system-jobs/<job>。
func (p *PiInvoker) jobAuditDir(req DiagnoseRequest) string {
	base := projectStateDir(req.ProjectID)
	if base == "" {
		return ""
	}
	key := strings.TrimSpace(req.JobID)
	if key == "" {
		key = diagnoseRunID(req)
	}
	return filepath.Join(base, systemJobsAuditDir, sanitize(key))
}

// linkJobRun 把作业 ↔ 诊断 run ↔ journal 目录写进 system_jobs。
//
// 这条反查链接是用量归属的落点：用量行带 run_id，作业行有同一个 run_id。
func (p *PiInvoker) linkJobRun(req DiagnoseRequest, runID, jobDir string) {
	if p == nil || p.Store == nil || strings.TrimSpace(req.JobID) == "" {
		return
	}
	_ = p.Store.Write(func(tx *sql.Tx) error {
		return db.SetSystemJobRun(tx, req.JobID, runID, "", jobDir)
	})
}

// writeAuditSummary 把「这次诊断怎么结束的」写成可读 JSON。
//
// 失败也写：重排后只有作业结果里的一行错误，证据要在审计目录里找得到。
func (p *PiInvoker) writeAuditSummary(dir string, req DiagnoseRequest, runID string,
	res piRunResult, stream piDiagnoseStream, usageWritten int, usageErr error, journal string) {
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	payload := map[string]any{
		"job_id":          req.JobID,
		"project_id":      req.ProjectID,
		"task_id":         req.TaskID,
		"kind":            req.Kind,
		"run_id":          runID,
		"node_id":         p.NodeID,
		"mode":            "diagnose",
		"write_scope":     []string{},
		"exit_code":       res.ExitCode,
		"result_seen":     stream.ResultSeen,
		"ok":              stream.OK,
		"reason":          stream.Reason,
		"error_category":  stream.ErrorCategory,
		"error_message":   stream.ErrorMessage,
		"sdk_version":     stream.SDKVersion,
		"protocol_ok":     stream.ProtocolOK,
		"mode_ok":         stream.ModeOK,
		"unknown_types":   stream.UnknownTypes,
		"malformed_lines": stream.Malformed,
		"usage_calls":     usageWritten,
		"journal":         journal,
		"stderr":          truncate(strings.TrimSpace(res.Stderr), 1200),
		"written_at":      p.now().Format(time.RFC3339Nano),
	}
	if len(stream.Invalid) > 0 {
		payload["invalid"] = stream.Invalid
	}
	if len(stream.Evidence) > 0 {
		payload["evidence"] = stream.Evidence
	}
	if usageErr != nil {
		// 用量写失败不改诊断结论，但必须留痕（补录口径见契约 §11）。
		payload["usage_error"] = usageErr.Error()
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	b = append(b, '\n')
	_ = os.WriteFile(filepath.Join(dir, diagnoseAuditFile), b, 0o600)
}

// writePiJournal 逐字节保留运行时事件流与 stderr，返回 journal 路径。
func writePiJournal(dir string, res piRunResult) string {
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, diagnoseJournalFile)
	var b strings.Builder
	b.WriteString(res.Stdout)
	if strings.TrimSpace(res.Stderr) != "" {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("# stderr\n")
		b.WriteString(res.Stderr)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return ""
	}
	return path
}

// recordUsage 把逐次调用用量写进观测表，并归属到「诊断 + 本次作业」。
//
// 没有 token 的调用不写用量行（契约 P172）：宁可缺席，也不填 0。
func (p *PiInvoker) recordUsage(ctx context.Context, req DiagnoseRequest, runID string, facts []observe.Facts) (int, error) {
	if p == nil || p.Store == nil || len(facts) == 0 {
		return 0, nil
	}
	writer := &dbstore.Store{DB: p.Store, ProjectID: req.ProjectID}
	written := 0
	for _, f := range facts {
		f.ProjectID = firstNonEmptyString(f.ProjectID, req.ProjectID)
		f.RunID = runID
		f.Generation = 1
		f.Purpose = observe.PurposeDiagnose
		if strings.TrimSpace(f.NodeID) == "" {
			f.NodeID = p.NodeID
		}
		if strings.TrimSpace(f.Model) == "" {
			f.Model = p.Model
		}
		if strings.TrimSpace(f.SessionID) == "" {
			// 运行时一次进程只服务一次 Run：用 run 派生会话身份（可重放、不靠模型自述）。
			f.SessionID = "run:" + runID
		}
		if err := writer.WriteFacts(ctx, f); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// parsePiDiagnoseStream 解析 NDJSON 事件流。
//
// 未知类型与坏行只计数、不猜语义（契约 P142）；结构不符（契约／协议／模式／缺终态）
// 记进 Invalid，让调用方按 invalid_output 处理。
func parsePiDiagnoseStream(stdout string) piDiagnoseStream {
	var out piDiagnoseStream
	extractor, _ := observe.ExtractorFor(model.Protocol{Format: "pi-stream-json", Version: "1"})
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var env piEventEnvelope
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			out.Malformed++
			continue
		}
		if strings.TrimSpace(env.Contract) != piDiagnoseContract {
			out.Invalid = append(out.Invalid, fmt.Sprintf("第 %d 行契约不符（%q）", line, env.Contract))
			continue
		}
		switch strings.TrimSpace(env.Type) {
		case "result":
			var p piResultPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				out.Invalid = append(out.Invalid, fmt.Sprintf("第 %d 行 result 载荷无法解析: %v", line, err))
				continue
			}
			out.ResultSeen = true
			out.OK = p.OK
			out.Text = p.Text
			out.Reason = strings.TrimSpace(p.Reason)
			out.Evidence = append(out.Evidence, p.Evidence...)
		case "error":
			var e piErrorPayload
			if err := json.Unmarshal(env.Payload, &e); err == nil {
				if out.ErrorCategory == "" {
					out.ErrorCategory = strings.TrimSpace(e.Category)
				}
				if out.ErrorMessage == "" {
					out.ErrorMessage = strings.TrimSpace(e.Message)
				}
			}
		case "version_report":
			var v piVersionPayload
			if err := json.Unmarshal(env.Payload, &v); err == nil {
				out.SDKVersion = strings.TrimSpace(v.SDKVersion)
				out.ProtocolOK = strings.TrimSpace(v.Protocol.Format) == "pi-stream-json" &&
					strings.TrimSpace(v.Protocol.Version) == "1"
				out.ModeOK = strings.TrimSpace(v.Mode) == "diagnose"
				if !out.ProtocolOK {
					out.Invalid = append(out.Invalid, "运行时自报协议不是 pi-stream-json/1")
				}
				if !out.ModeOK {
					out.Invalid = append(out.Invalid, "运行时回显模式不是 diagnose")
				}
			}
		case "usage":
			if extractor == nil {
				continue
			}
			facts, ok := extractor.Extract(observe.Record{
				Payload: []byte(raw), Seq: 0, Line: line, ReceivedAt: time.Now().UTC(),
			})
			if !ok {
				out.Malformed++
				continue
			}
			if facts.HasUsage() {
				out.Usage = append(out.Usage, facts)
			}
		case "session_started", "session_ended", "stage_hint", "tool_call", "tool_result", "tool_denied":
			// 已知但不承载终态或用量：journal 已保留原文。
		default:
			out.UnknownTypes++
		}
	}
	return out
}

// diagnoseCategory 把来源给的错误类别收口到契约 §7.2 的封闭集合。
func diagnoseCategory(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "auth", "quota", "rate_limit", "timeout", "tool_denied", "invalid_output", "crash", "env", "unknown":
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return ""
}

// piDiagnoseEnvValues 按白名单从宿主环境取值。
func piDiagnoseEnvValues() []string {
	out := make([]string, 0, len(piDiagnoseEnv))
	for _, name := range piDiagnoseEnv {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// defaultPiExec 用 argv 数组起进程（禁止 shell 解释器，契约 P152）。
func defaultPiExec(ctx context.Context, argv []string, cwd string, env []string, stdin string) (piRunResult, error) {
	if len(argv) == 0 {
		return piRunResult{}, errors.New("argv 为空")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := piRunResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			// 被超时／取消杀掉也是 ExitError：调用方按 ctx 区分，这里不吞掉上下文错误。
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			return res, nil
		}
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, err
	}
	return res, nil
}

// firstNonEmptyString 返回第一个非空串（去空白后）。
func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
