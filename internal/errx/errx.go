package errx

import "fmt"

// Error carries a Next-step hint for LLM callers.
type Error struct {
	Msg  string
	Next string
	Code int
}

func (e *Error) Error() string {
	if e.Next == "" {
		return "Error: " + e.Msg
	}
	return fmt.Sprintf("Error: %s\nNext: %s", e.Msg, e.Next)
}

func New(msg string, next string) *Error {
	return &Error{Msg: msg, Next: next, Code: 1}
}

func Wrap(msg string, next string, err error) *Error {
	if err == nil {
		return New(msg, next)
	}
	return New(fmt.Sprintf("%s: %v", msg, err), next)
}
