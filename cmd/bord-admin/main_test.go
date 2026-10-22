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

	"bord/internal/adminapp"
	"bord/internal/client"

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
	runProgram = func(_ context.Context, model tea.Model, _ io.Writer) (tea.Model, error) { return model, nil }
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

func TestPublicServerPrecedenceAndValidation(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", directory)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("BORD_ADMIN_CREDENTIAL", "admin.secret")
	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"root","role":"administrator","expires_at":"2027-01-01T00:00:00Z"}`)
	}))
	defer management.Close()
	t.Setenv("BORD_ADMIN_MANAGEMENT", management.URL)
	t.Setenv("BORD_SERVER", "https://env.example")
	original := runProgram
	t.Cleanup(func() { runProgram = original })
	runProgram = func(_ context.Context, model tea.Model, _ io.Writer) (tea.Model, error) { return model, nil }
	if err := run(context.Background(), []string{"--server", "https://flag.example"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "bord", "admin.json"))
	if err != nil || !strings.Contains(string(data), "https://flag.example") || strings.Contains(string(data), "env.example") {
		t.Fatalf("state=%s err=%v", data, err)
	}
	if err := run(context.Background(), []string{"--server", "://bad"}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "invalid server URL") {
		t.Fatalf("invalid server error=%v", err)
	}
}

func TestIntegratedPresentationPersistsOnlyInAdminState(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", directory)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("BORD_ADMIN_CREDENTIAL", "admin.secret")
	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"id":"root","role":"administrator","expires_at":"2027-01-01T00:00:00Z"}`)
	}))
	defer management.Close()
	t.Setenv("BORD_ADMIN_MANAGEMENT", management.URL)
	userState, err := client.NewState("https://old-public.example")
	if err != nil {
		t.Fatal(err)
	}
	userState.Theme = "amber"
	userStore := client.NewStore(filepath.Join(directory, "bord", "state.json"))
	if err = userStore.Save(userState); err != nil {
		t.Fatal(err)
	}
	original := runProgram
	t.Cleanup(func() { runProgram = original })
	runProgram = func(_ context.Context, model tea.Model, _ io.Writer) (tea.Model, error) {
		app := model.(adminapp.Model)
		updated, _ := app.Update(tea.KeyMsg{Type: tea.KeyF3})
		return updated, nil
	}
	if err = run(context.Background(), []string{"--theme", "green", "--server", "https://new-public.example"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	loadedUser, err := userStore.Load()
	if err != nil || loadedUser.Theme != "amber" || loadedUser.SessionSecret == "" {
		t.Fatalf("user state=%#v err=%v", loadedUser, err)
	}
	adminData, err := os.ReadFile(filepath.Join(directory, "bord", "admin.json"))
	if err != nil || !strings.Contains(string(adminData), `"theme": "green"`) || !strings.Contains(string(adminData), `"mode": "user"`) || strings.Contains(string(adminData), loadedUser.SessionSecret) {
		t.Fatalf("admin state=%s err=%v", adminData, err)
	}
}
