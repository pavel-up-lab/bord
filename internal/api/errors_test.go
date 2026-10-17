package api

import (
	"errors"
	"net/http"
	"testing"

	"bord/internal/domain"
)

func TestErrorMappingContract(t *testing.T) {
	tests := []struct {
		code   domain.Code
		status int
	}{
		{domain.CodeValidation, http.StatusBadRequest},
		{domain.CodeBoardNotFound, http.StatusNotFound},
		{domain.CodeThreadClosed, http.StatusConflict},
		{domain.CodeRateLimited, http.StatusTooManyRequests},
		{domain.CodePayloadTooLarge, http.StatusRequestEntityTooLarge},
		{domain.CodeServiceUnavailable, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		status, body := MapError(domain.NewError(tt.code, "", nil), "request-123")
		if status != tt.status || body.Code != tt.code || body.RequestID != "request-123" {
			t.Errorf("%s mapped to %d %#v", tt.code, status, body)
		}
	}
	status, body := MapError(errors.New("secret database error"), "request-123")
	if status != http.StatusInternalServerError || body.Message != "internal server error" {
		t.Fatalf("unsafe internal mapping: %#v", body)
	}
}
