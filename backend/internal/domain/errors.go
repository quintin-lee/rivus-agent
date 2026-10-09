package domain

import "errors"

// 领域错误分类：调用方据此决定重试 / 中断 / 人工核查。
var (
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("conflict")
	ErrForbidden      = errors.New("forbidden")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrBadRequest     = errors.New("bad request")
	ErrBudgetExceeded = errors.New("budget exceeded")
	ErrTimeout        = errors.New("timeout")
	ErrCancelled      = errors.New("cancelled")
	ErrNeedsApproval  = errors.New("needs approval")
	ErrRetryable      = errors.New("retryable")
	ErrNonRetryable   = errors.New("non-retryable")
)

// CodedError 携带机器可读 code 与是否可重试标记。
type CodedError struct {
	Code      string
	Msg       string
	Retryable bool
	Err       error
}

func (e *CodedError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Msg + ": " + e.Err.Error()
	}
	return e.Code + ": " + e.Msg
}

func (e *CodedError) Unwrap() error { return e.Err }

func Wrap(code, msg string, retryable bool, err error) *CodedError {
	return &CodedError{Code: code, Msg: msg, Retryable: retryable, Err: err}
}
