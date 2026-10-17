package api

import (
	"net/http"

	"bord/internal/domain"
)

type ErrorResponse struct {
	Code       domain.Code       `json:"code"`
	Message    string            `json:"message"`
	RequestID  string            `json:"request_id"`
	Fields     map[string]string `json:"fields,omitempty"`
	RetryAfter int               `json:"retry_after,omitempty"`
}

func MapError(err error, requestID string) (int, ErrorResponse) {
	code := domain.ErrorCode(err)
	status, message := http.StatusInternalServerError, "internal server error"
	switch code {
	case domain.CodeValidation:
		status, message = http.StatusBadRequest, "request validation failed"
	case domain.CodeBoardNotFound, domain.CodeThreadNotFound, domain.CodePostNotFound, domain.CodeAttachmentNotFound:
		status, message = http.StatusNotFound, "resource not found"
	case domain.CodeUnauthorized:
		status, message = http.StatusUnauthorized, "authentication required"
	case domain.CodeForbidden:
		status, message = http.StatusForbidden, "operation not allowed"
	case domain.CodeThreadClosed, domain.CodeIdempotencyConflict, domain.CodeConflict:
		status, message = http.StatusConflict, "operation conflicts with current state"
	case domain.CodeRateLimited:
		status, message = http.StatusTooManyRequests, "rate limit exceeded"
	case domain.CodeChallengeRequired, domain.CodeInvalidChallenge:
		status, message = http.StatusForbidden, "proof of work required or invalid"
	case domain.CodePayloadTooLarge:
		status, message = http.StatusRequestEntityTooLarge, "request body is too large"
	}
	return status, ErrorResponse{Code: code, Message: message, RequestID: requestID}
}
