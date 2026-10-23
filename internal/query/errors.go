package query

import (
	"errors"
	"fmt"
)

// 错误码取 HTTP 契约 §6 的 error.code；本包不 import net/http，
// 状态码与错误体由接入层按契约映射（这两个码都是 422）。
const (
	// CodeInvalidPagination：limit 越界或游标无法解析。
	// 静默截断、静默从头重来都算错误——前者少给数据，后者会让客户端看到重复记录。
	CodeInvalidPagination = "invalid_pagination"
	// CodeUnknownEnum：过滤词不在封闭集合里。
	// 不把它当成「零匹配」：否则拼错状态名会得到一页空结果，看起来像「确实没有」。
	CodeUnknownEnum = "unknown_enum"
)

// Error 是查询层的语义错误：只描述「哪个字段、违反了什么」，不带 HTTP 细节。
type Error struct {
	Code  string `json:"code"`
	Field string `json:"field,omitempty"`
	Msg   string `json:"message"`
}

// Error 实现 error。
func (e *Error) Error() string {
	switch {
	case e.Field != "" && e.Code != "":
		return e.Code + " " + e.Field + ": " + e.Msg
	case e.Code != "":
		return e.Code + ": " + e.Msg
	default:
		return e.Msg
	}
}

// IsCode 报告 err 是否是给定契约码。
func IsCode(err error, code string) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Code == code
	}
	return false
}

func invalidPagination(field, format string, args ...any) *Error {
	return &Error{Code: CodeInvalidPagination, Field: field, Msg: fmt.Sprintf(format, args...)}
}

func unknownEnum(field, value string) *Error {
	return &Error{Code: CodeUnknownEnum, Field: field, Msg: fmt.Sprintf("未知取值 %q", value)}
}
