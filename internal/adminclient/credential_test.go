package adminclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCredentialPriorityAndWhitespace(t *testing.T) {
	directory := t.TempDir()
	explicit := filepath.Join(directory, "explicit.credential")
	fromEnvFile := filepath.Join(directory, "env.credential")
	if err := os.WriteFile(explicit, []byte("explicit.secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fromEnvFile, []byte("file.secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORD_ADMIN_CREDENTIAL_FILE", fromEnvFile)
	t.Setenv("BORD_ADMIN_CREDENTIAL", "environment.secret")
	if got, err := LoadCredential(explicit); err != nil || got != "environment.secret" {
		t.Fatalf("environment priority=%q err=%v", got, err)
	}
	t.Setenv("BORD_ADMIN_CREDENTIAL", "")
	if _, err := LoadCredential(explicit); err == nil {
		t.Fatal("present empty environment credential was ignored")
	}
	if err := os.Unsetenv("BORD_ADMIN_CREDENTIAL"); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadCredential(explicit); err != nil || got != "explicit.secret" {
		t.Fatalf("explicit priority=%q err=%v", got, err)
	}
	if got, err := LoadCredential(""); err != nil || got != "file.secret" {
		t.Fatalf("file environment priority=%q err=%v", got, err)
	}
}

func TestCredentialFileSecurityAndRedaction(t *testing.T) {
	directory := t.TempDir()
	unsafe := filepath.Join(directory, "unsafe.credential")
	if err := os.WriteFile(unsafe, []byte("top-secret.value"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCredentialFile(unsafe); err == nil || strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("unsafe permission error=%v", err)
	}
	secure := filepath.Join(directory, "secure.credential")
	if err := os.WriteFile(secure, []byte("top-secret.value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link.credential")
	if err := os.Symlink(secure, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readCredentialFile(link); err == nil || strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("symlink error=%v", err)
	}
	if got, err := readCredentialFile(secure); err != nil || got != "top-secret.value" {
		t.Fatalf("secure credential=%q err=%v", got, err)
	}
	if err := os.WriteFile(secure, []byte("embedded secret value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCredentialFile(secure); err == nil || strings.Contains(err.Error(), "embedded") {
		t.Fatalf("whitespace error=%v", err)
	}
}
