package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagementBanRoundTripAndCredentialPermissions(t *testing.T) {
	credentialPath := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credentialPath, []byte("operator.secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credential, err := ReadCredentialFile(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer operator.secret" {
			t.Error("missing bearer")
		}
		if calls == 1 {
			if r.Method != http.MethodPost || r.URL.Path != "/admin/v1/bans" {
				t.Errorf("request=%s %s", r.Method, r.URL.Path)
			}
			var body BanRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Subject != "abcd" {
				t.Errorf("body=%+v err=%v", body, err)
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"00000001"}`))
			return
		}
		if r.Method != http.MethodDelete || r.URL.Path != "/admin/v1/bans/00000001" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	id, err := ManagementBan(context.Background(), server.Client(), server.URL, credential, BanRequest{SubjectType: "lookup", Subject: "abcd", Operations: []string{"create_post"}, ExpiresAt: time.Now().Add(time.Hour), Reason: "abuse"})
	if err != nil || id != "00000001" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if err := ManagementUnban(context.Background(), server.Client(), server.URL, credential, id, "appeal accepted"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
	if err := os.Chmod(credentialPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredentialFile(credentialPath); err == nil {
		t.Fatal("insecure credential permissions accepted")
	}
}
