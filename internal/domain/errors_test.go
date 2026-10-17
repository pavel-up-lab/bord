package domain

import (
	"errors"
	"testing"
)

func TestStableErrorCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Code
	}{
		{"validation", NewError(CodeValidation, "body", nil), "validation_failed"},
		{"not found", NewError(CodeBoardNotFound, "", nil), "board_not_found"},
		{"closed", NewError(CodeThreadClosed, "", nil), "thread_closed"},
		{"idempotency", NewError(CodeIdempotencyConflict, "", nil), "idempotency_conflict"},
		{"unknown", errors.New("database details"), "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ErrorCode(tt.err); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
