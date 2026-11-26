package codebuddy

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// 结果记录里的 subtype 取值（实测到的两个）。别的取值不猜（P142）。
const (
	subtypeSuccess        = "success"
	subtypeErrorExecution = "error_during_execution"
)

// Interpret 实现 ports.Interpret：判定一次运行的结果。
//
// 判定的唯一依据是 `result` 记录（C11/C12）：
//
//	result.subtype == success 且 is_error == false 且正文非空 → completed
//	subtype == error_during_execution／is_error == true        → failed
//	缺 result 记录                                              → unknown（X02）
//	subtype=success 但正文为空                                  → unknown（X03）
//	未识别的 subtype                                            → unknown（P142）
//
// **退出码 0 不是成功依据**（P141、S08：真实认证失败就是退出码 0）。
// 退出码只在没有结果记录时用来区分「进程崩了」（失败）与「什么都没说」（待核实），
// 以及与结果记录冲突时把结论降级为待核实。
//
// 未知字段（例如 X04 的 cli_version、future_usage_field）不参与判定：
// 它们不在 sourceRecord 里，解析就会丢掉，判定与实测样本保持一致。
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
	if msg := strings.TrimSpace(req.ApplicationError); msg != "" {
		res.Outcome = model.OutcomeFailed
		res.ResultText = msg
		res.Quality = model.QualityObserved
		return res, nil
	}

	if len(req.FinalResult) == 0 {
		return interpretWithoutResult(req, res)
	}
	return interpretResultRecord(req, res)
}

// interpretWithoutResult 处理「没有最终结果记录」的两种情况。
//
// 退出码非 0 = 进程确实失败了（S06 启动失败即此形态）；退出码 0 或缺失 = 什么都不能判。
func interpretWithoutResult(req ports.InterpretRequest, res model.InterpretResult) (model.InterpretResult, error) {
	if req.ExitCode != nil && *req.ExitCode != 0 {
		res.Outcome = model.OutcomeFailed
		res.Quality = model.QualityObserved
		res.ResultText = "进程退出码 " + strconv.Itoa(*req.ExitCode) + "，且没有 result 记录"
		return res, nil
	}
	return res, model.PortFailuref(model.PortErrMissingResult,
		"没有 result 记录：退出码 %s 不能作为完成依据（S08 实测退出码 0 也可能是认证失败），本次运行保持待核实",
		exitCodeText(req.ExitCode))
}

// interpretResultRecord 按结果记录判定。
func interpretResultRecord(req ports.InterpretRequest, res model.InterpretResult) (model.InterpretResult, error) {
	var rec sourceRecord
	if err := json.Unmarshal(req.FinalResult, &rec); err != nil {
		return res, model.PortFailuref(model.PortErrProtocolUnknown,
			"最终结果不是单条 JSON 记录：%v；json 模式的数组结构属另一种协议（C05），不按同一套规则解释", err)
	}
	if rec.Type != "result" {
		return res, model.PortFailuref(model.PortErrProtocolUnknown,
			"最终结果的记录类型是 %q，不是 result：无法据此判定完成", rec.Type)
	}

	switch rec.Subtype {
	case subtypeSuccess:
		return interpretSuccessRecord(req, res, rec)
	case subtypeErrorExecution:
		return interpretErrorRecord(req, res, rec)
	default:
		// P142：未识别结果不得标成功，也不得标失败——证据不足就是待核实。
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
		// X03：subtype=success 不足以判定交付，空正文必须降级为待核实。
		return res, model.PortFailuref(model.PortErrMissingResult,
			"subtype=success 但正文为空：成功标记不等于交付，本次运行保持待核实（X03）")
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
// 认证失败单独归类（C14）：它是「需要人去登录」，不是代码/任务失败，
// 调用方要能把它挡住而不是不停重试同一条规格。
func interpretErrorRecord(req ports.InterpretRequest, res model.InterpretResult, rec sourceRecord) (model.InterpretResult, error) {
	res.Outcome = model.OutcomeFailed
	res.Quality = model.QualityObserved
	res.ResultText = joinErrors(rec)
	if res.ResultText == "" {
		res.ResultText = strings.TrimSpace(rec.Result)
	}

	if authRequired(rec.Errors) {
		return res, model.PortFailuref(model.PortErrAuthRequired,
			"来源报告未登录（%s）：这是「需要登录」，不是任务失败；退出码 %s 与它无关",
			clip(res.ResultText), exitCodeText(req.ExitCode))
	}
	return res, model.PortFailuref(model.PortErrApplication,
		"来源报告执行期错误：%s", clip(res.ResultText))
}

// authRequired 判断错误文本是否属于「未登录」。
//
// 按前缀、忽略大小写匹配：证据里没有机器可判的错误码（C14/C26），
// 全等匹配会在文案微调后静默失效。
func authRequired(errs []string) bool {
	for _, e := range errs {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(e)), strings.ToLower(authRequiredPrefix)) {
			return true
		}
	}
	return false
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
