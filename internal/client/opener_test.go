package client

import (
	"strings"
	"testing"
)

func TestOpenerEnvironmentRemovesAdminCredential(t *testing.T) {
	got := openerEnvironment([]string{"PATH=/bin", "BORD_ADMIN_CREDENTIAL=top-secret", "bord_admin_credential=other-secret", "SAFE=value"})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "secret") || !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "SAFE=value") {
		t.Fatalf("opener environment=%q", joined)
	}
}
