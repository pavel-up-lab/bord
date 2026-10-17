package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestSensitiveValuesNeverSerialized(t *testing.T) {
	var out bytes.Buffer
	log := JSON(&out, slog.LevelDebug)
	log.With("session_secret", "session-value").Error("publish failed",
		"request_id", "req-safe",
		"raw_ip", "192.0.2.1",
		"post_body", "private words",
		"bearer", "bearer-value",
		"delete_token", "delete-value",
		"details", slog.GroupValue(slog.String("access_token", "access-value")),
		"error_code", "validation_failed",
	)
	got := out.String()
	for _, forbidden := range []string{"192.0.2.1", "private words", "bearer-value", "delete-value", "access-value", "session-value"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("log leaked %q: %s", forbidden, got)
		}
	}
	for _, required := range []string{"req-safe", "validation_failed"} {
		if !strings.Contains(got, required) {
			t.Errorf("log omitted %q: %s", required, got)
		}
	}
}
