package domain

import "errors"

// Code is a stable public classification; messages and wrapped causes are not contracts.
type Code string

const (
	CodeInternal            Code = "internal_error"
	CodeValidation          Code = "validation_failed"
	CodeBoardNotFound       Code = "board_not_found"
	CodeThreadNotFound      Code = "thread_not_found"
	CodePostNotFound        Code = "post_not_found"
	CodeAttachmentNotFound  Code = "attachment_not_found"
	CodeThreadClosed        Code = "thread_closed"
	CodeUnauthorized        Code = "unauthorized"
	CodeForbidden           Code = "forbidden"
	CodeRateLimited         Code = "rate_limited"
	CodeChallengeRequired   Code = "challenge_required"
	CodeInvalidChallenge    Code = "invalid_challenge"
	CodeIdempotencyConflict Code = "idempotency_conflict"
	CodePayloadTooLarge     Code = "payload_too_large"
	CodeConflict            Code = "conflict"
)

type Error struct {
	Code  Code
	Field string
	Cause error
}

func (e *Error) Error() string { return string(e.Code) }
func (e *Error) Unwrap() error { return e.Cause }

func NewError(code Code, field string, cause error) error {
	return &Error{Code: code, Field: field, Cause: cause}
}

func ErrorCode(err error) Code {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return CodeInternal
}
