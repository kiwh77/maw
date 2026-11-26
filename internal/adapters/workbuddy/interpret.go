package workbuddy

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// 结果记录里的 subtype 取值（家族协议两个已知取值）。
const (
	subtypeSuccess        = "success"
	subtypeErrorExecution = "error_during_execution"
)

// AuthHint 是「认证缺失」时给操作者的人工接入与处理信息。
//
// 为什么必须给一段人话：两款 WorkBuddy 的登录态是各自 App 内的账号，maw 既不能读
// 桌面凭据也不能代登录（Non-goals）。认证缺失时唯一正确的处理是「请人去登录」，
// 所以错误文案要写清在哪个包、用什么命令、maw 不会做什么。
func AuthHint(d Distribution) string {
	return fmt.Sprintf("请在 %s %s（%s，bundle %s）里用该包自己的账号执行 /login 完成登录，"+
		"然后用同一入口重新探测再派发；maw 不代登录、不读取桌面凭据、不接管已有会话",
		d.AppName, d.AppVersion, d.Kind, d.BundleID)
}

// Interpret 实现 ports.Interpret：判定一次运行的结果。
//
// 判定顺序（每一条都能独立成立，不靠下一条兜底）：
//
//	超时                                   → timed_out
//	应用错误（含 stderr 原文）              → failed；文本命中认证前缀则 auth_required
//	没有最终结果记录                        → 退出码非 0 判 failed，退出码 0 判 unknown（W10）
//	有最终结果记录但形状未观测（W09）        → unknown + protocol_unknown，不判完成
//	有最终结果记录且形状已观测              → 按 subtype／is_error／正文判定
//
// **退出码 0 不是成功依据**（W10：两款 WorkBuddy 的认证失败就是退出码 0 + stdout 空）。
func (a *Adapter) Interpret(ctx context.Context, req ports.InterpretRequest) (model.InterpretResult, error) {
	if vs := req.Validate(); len(vs) > 0 {
		return model.InterpretResult{}, model.PortFailuref(model.PortErrInvalidSpec,
			"判定请求不合法：%v", model.Err(vs))
	}

	res := model.InterpretResult{
		Outcome:   model.OutcomeUnknown,
		Quality:   model.QualityUnknown,
		Artifacts: req.Artifacts,
	}

	// 超时是确定结论：进程没在预算内跑完，与来源说了什么无关。
	if req.TimedOut {
		res.Outcome = model.OutcomeTimedOut
		res.Quality = model.QualityObserved
		return res, nil
	}

	// 应用错误优先于结果记录：进程层面已经异常，就不能因为「顺手写了一条 success」判完成。
	//
	// 这一段也是本包认证缺失的主路径：两包的 `Authentication required. Please use /login …`
	// 落在 stderr，调用方以 ApplicationError 传进来。认不出来就会被当成普通应用错误，
	// 于是同一份规格被反复重试——这正是 W11 要避免的。
	if msg := strings.TrimSpace(req.ApplicationError); msg != "" {
		res.Outcome = model.OutcomeFailed
		res.ResultText = msg
		res.Quality = model.QualityObserved
		if authRequiredText(msg) {
			return res, model.PortFailuref(model.PortErrAuthRequired,
				"来源报告未登录（%s）：这是「需要人去登录」，不是任务失败；退出码 %s 与它无关。%s",
				clip(msg), exitCodeText(req.ExitCode), a.authHint())
		}
		return res, model.PortFailuref(model.PortErrApplication, "来源报告应用错误：%s", clip(msg))
	}

	if len(req.FinalResult) == 0 {
		return interpretWithoutResult(req, res)
	}
	return a.interpretResultRecord(req, res)
}

// interpretWithoutResult 处理「没有最终结果记录」的两种情况。
func interpretWithoutResult(req ports.InterpretRequest, res model.InterpretResult) (model.InterpretResult, error) {
	if req.ExitCode != nil && *req.ExitCode != 0 {
		res.Outcome = model.OutcomeFailed
		res.Quality = model.QualityObserved
		res.ResultText = "进程退出码 " + strconv.Itoa(*req.ExitCode) + "，且没有结果记录"
		return res, nil
	}
	return res, model.PortFailuref(model.PortErrMissingResult,
		"没有结果记录：退出码 %s 不能作为完成依据（两包实测退出码 0 也可能是认证失败：stdout 空、stderr 有错误）；"+
			"请把 stderr 原文作为应用错误交给判定，或本次运行保持待核实",
		exitCodeText(req.ExitCode))
}

// interpretResultRecord 按结果记录判定；形状未观测时拒绝判完成。
func (a *Adapter) interpretResultRecord(req ports.InterpretRequest, res model.InterpretResult) (model.InterpretResult, error) {
	var rec sourceRecord
	if err := json.Unmarshal(req.FinalResult, &rec); err != nil {
		return res, model.PortFailuref(model.PortErrProtocolUnknown,
			"最终结果不是单条 JSON 对象：%v；本形态只产出单条结果，数组结构属另一种协议", err)
	}
	if rec.Type != "result" {
		return res, model.PortFailuref(model.PortErrProtocolUnknown,
			"最终结果的记录类型是 %q，不是 result：无法据此判定完成", rec.Type)
	}

	// 形状闸门：没有样本就不判完成。
	state, ok := a.capabilityState(capResultRecord)
	if !ok {
		return res, model.PortFailure(model.PortErrCapabilityMissing,
			"尚未成功探测：判定完成必须先确认这个入口、这个包的记录形状能力状态")
	}
	if state != model.CapabilityObserved {
		return res, model.PortFailuref(model.PortErrProtocolUnknown,
			"%s 的能力状态是 %s：单条 json 结果的字段语义没有样本（两包均未完成过回合），"+
				"按未验证语义判完成是最坏的一类误判，本次运行保持待核实", capResultRecord, state)
	}

	switch rec.Subtype {
	case subtypeSuccess:
		return interpretSuccessRecord(req, res, rec)
	case subtypeErrorExecution:
		return interpretErrorRecord(req, res, rec)
	default:
		return res, model.PortFailuref(model.PortErrProtocolUnknown,
			"未识别的 result.subtype %q：既不判完成也不判失败，保持待核实", rec.Subtype)
	}
}

// interpretSuccessRecord 处理 subtype=success。
func interpretSuccessRecord(req ports.InterpretRequest, res model.InterpretResult, rec sourceRecord) (model.InterpretResult, error) {
	if rec.IsError != nil && *rec.IsError {
		// 自相矛盾：subtype 说成功，标志位说错误。按错误处理，但质量降级——记录本身不可信。
		res.Outcome = model.OutcomeFailed
		res.Quality = model.QualityUnknown
		res.ResultText = joinErrors(rec)
		return res, model.PortFailuref(model.PortErrApplication,
			"结果记录自相矛盾：subtype=success 但 is_error=true（%s）", clip(joinErrors(rec)))
	}

	if strings.TrimSpace(rec.Result) == "" {
		return res, model.PortFailure(model.PortErrMissingResult,
			"subtype=success 但正文为空：成功标记不等于交付，本次运行保持待核实")
	}

	// 退出码与结果记录冲突时不判完成：两组证据打架，说明有一方不可信。
	if req.ExitCode != nil && *req.ExitCode != 0 {
		res.ResultText = rec.Result
		return res, model.PortFailuref(model.PortErrProtocolUnknown,
			"结果记录说成功，但进程退出码是 %d：证据冲突，保持待核实而不是取其中一方", *req.ExitCode)
	}

	res.Outcome = model.OutcomeCompleted
	res.Quality = model.QualityObserved
	res.ResultText = rec.Result
	return res, nil
}

// interpretErrorRecord 处理 subtype=error_during_execution。
//
// 认证失败单独归类：它是「需要人去登录」，不是代码／任务失败，
// 调用方要能把它挡住而不是不停重试同一条规格。
func interpretErrorRecord(req ports.InterpretRequest, res model.InterpretResult, rec sourceRecord) (model.InterpretResult, error) {
	res.Outcome = model.OutcomeFailed
	res.Quality = model.QualityObserved
	res.ResultText = joinErrors(rec)
	if res.ResultText == "" {
		res.ResultText = strings.TrimSpace(rec.Result)
	}

	if authRequiredText(res.ResultText) {
		return res, model.PortFailuref(model.PortErrAuthRequired,
			"来源报告未登录（%s）：这是「需要登录」，不是任务失败；退出码 %s 与它无关",
			clip(res.ResultText), exitCodeText(req.ExitCode))
	}
	return res, model.PortFailuref(model.PortErrApplication,
		"来源报告执行期错误：%s", clip(res.ResultText))
}

// authRequiredText 判断一段来源文本是否属于「未登录」。
//
// 按前缀、忽略大小写匹配：证据里没有机器可判的错误码，
// 全等匹配会在文案微调后静默失效。
func authRequiredText(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), strings.ToLower(authRequiredPrefix)) {
			return true
		}
	}
	return false
}

// authHint 取当前节点所属发行包的人工处理指引。
//
// 探测过就用探测确认的那个包；还没探测过就按配置里的入口现判一次。
// 都判不出来时给通用指引——文案里不写包名，总比写错包名好。
func (a *Adapter) authHint() string {
	a.mu.Lock()
	last := a.last
	a.mu.Unlock()
	if last != nil {
		return AuthHint(last.dist)
	}
	if dist, err := Detect(a.cfg.EntryPath); err == nil {
		return AuthHint(dist)
	}
	return "请在对应的 WorkBuddy 应用里用该包自己的账号执行 /login 完成登录，再用同一入口重新探测；" +
		"maw 不代登录、不读取桌面凭据、不接管已有会话"
}

func joinErrors(rec sourceRecord) string {
	if len(rec.Errors) == 0 {
		return ""
	}
	return strings.Join(rec.Errors, "; ")
}

func exitCodeText(code *int) string {
	if code == nil {
		return "缺失"
	}
	return strconv.Itoa(*code)
}
