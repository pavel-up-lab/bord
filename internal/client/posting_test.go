package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"bord/api/generated"
	"bord/internal/security"
)

func TestPostingAPIPersistsCredentialAcrossUnknownOutcomeAndReplay(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	var firstVerifier string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SessionProof   string `json:"session_proof"`
			DeleteVerifier string `json:"delete_verifier"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		verifier, err := base64.RawURLEncoding.DecodeString(body.DeleteVerifier)
		if err != nil || !security.ValidateVerifierParameters(string(verifier), security.DefaultArgon2Params) {
			t.Errorf("invalid verifier: %v", err)
		}
		if len(body.SessionProof) < 32 {
			t.Error("missing session proof")
		}
		mu.Lock()
		requests++
		attempt := requests
		if attempt == 1 {
			firstVerifier = string(verifier)
		}
		mu.Unlock()
		if attempt == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close() // Simulate a committed request whose response was lost.
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"thread_id":"thread_1","post_id":"post_1"}`))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStore(path)
	state, err := NewState(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	transport, err := New(server.URL, server.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	posting, err := NewPostingAPI(transport, store, &state)
	if err != nil {
		t.Fatal(err)
	}
	body := "hello"
	if _, err = posting.CreateThread(context.Background(), "b", "idempotency-key-1", mapCreateThread(body)); err == nil {
		t.Fatal("expected unknown first outcome")
	}
	loaded, err := store.Load()
	if err != nil || len(loaded.PendingDeleteSecrets) != 1 || len(loaded.DeleteSecrets) != 0 {
		t.Fatalf("pending state not durable: %v %#v", err, loaded)
	}
	result, err := posting.CreateThread(context.Background(), "b", "idempotency-key-1", mapCreateThread(body))
	if err != nil || result.PostId != "post_1" {
		t.Fatalf("replay result=%#v err=%v", result, err)
	}
	loaded, err = store.Load()
	secret := loaded.DeleteSecrets["post_1"]
	if err != nil || secret == "" || len(loaded.PendingDeleteSecrets) != 0 {
		t.Fatalf("completed mapping not durable: %v %#v", err, loaded)
	}
	if !security.VerifySecret([]byte(secret), firstVerifier) {
		t.Fatal("retry did not retain the delete capability used by the first attempt")
	}
}

func mapCreateThread(body string) generated.CreateThread {
	return generated.CreateThread{Body: &body}
}
