package observability

// 传输层语义错误：只描述「哪个字段、错在哪」，HTTP 状态码由 respond.go 统一映射。
//
// 错误码取 HTTP 契约 §6 的形态（小写下划线）；422 用来表达「语法正确但语义不合法」，
// 409 留给事实冲突，两者不混用。
type (
	// ErrInvalid 是 422：字段缺失、格式错、取值不在封闭集合、窗口不可用。
	ErrInvalid struct {
		Code    string
		Field   string
		Message string
	}
	// ErrNotFound 是 404：资源不存在，或不属于本次请求的工程范围。
	//
	// 「不属于本工程」与「不存在」共用 404：区分它们等于给出存在性探针。
	ErrNotFound struct {
		Resource string
		Message  string
	}
	// ErrForbidden 是 403：凭据有效但无权（作用域不足等）。
	ErrForbidden struct {
		Code    string
		Message string
	}
)

func (e *ErrInvalid) Error() string   { return e.Code + " " + e.Field + ": " + e.Message }
func (e *ErrNotFound) Error() string  { return "not_found " + e.Resource + ": " + e.Message }
func (e *ErrForbidden) Error() string { return e.Code + ": " + e.Message }

func invalidField(code, field, message string) *ErrInvalid {
	return &ErrInvalid{Code: code, Field: field, Message: message}
}

// 稳定错误码：调用方按码分支，不按文案。
const (
	codeInvalidWindow   = "invalid_window"
	codeWindowTooLarge  = "window_too_large"
	codeInvalidTimezone = "invalid_timezone"
	codeInvalidFilter   = "invalid_filter"
	codeUnknownEnum     = "unknown_enum"
	codeInvalidPaging   = "invalid_pagination"
	codeCapabilityOff   = "capability_unavailable"
)
