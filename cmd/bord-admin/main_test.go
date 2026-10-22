package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bord/internal/admintui"

	tea "github.com/charmbracelet/bubbletea"
)

func TestVersionAndFlagsNeverExposeCredential(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "dev") {
		t.Fatalf("version=%q", stdout.String())
	}
	secret := "top-secret-command-line"
	err := run(context.Background(), []string{"--credential", secret}, &stdout, &stderr)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatalf("unsafe flag error=%v stderr=%q", err, stderr.String())
	}
}

func TestRunUsesManagementCredentialAndPersistsSafeDefaults(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", directory)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("BORD_ADMIN_CREDENTIAL", "admin.secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/v1/session" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer admin.secret" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"root","role":"administrator","expires_at":"2027-01-01T00:00:00Z"}`)
	}))
	defer server.Close()
	t.Setenv("BORD_ADMIN_MANAGEMENT", server.URL)
	original := runProgram
	t.Cleanup(func() { runProgram = original })
	runProgram = func(_ context.Context, model admintui.Model, _ io.Writer) (tea.Model, error) { return model, nil }
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "bord", "admin.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, server.URL) || strings.Contains(text, "admin.secret") || strings.Contains(strings.ToLower(text), "credential") {
		t.Fatalf("state=%s", text)
	}
}

func TestRunReportsIncompatibleManagementAPISafely(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BORD_ADMIN_CREDENTIAL", "admin.secret")
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	var output bytes.Buffer
	err := run(context.Background(), []string{"--management", server.URL}, io.Discard, &output)
	if err == nil || !strings.Contains(err.Error(), "несовместим") || strings.Contains(err.Error(), "admin.secret") {
		t.Fatalf("error=%v output=%q", err, output.String())
	}
}
