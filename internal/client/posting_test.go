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

func TestPostingAPIDeletesOnlyWithStoredAnonymousCapability(t *testing.T) {
	const secret = "anonymous-delete-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body generated.DeletePost
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DeleteToken != secret {
			t.Errorf("delete body=%#v err=%v", body, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	state, err := NewState(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	state.DeleteSecrets["post_1"] = secret
	if err = store.Save(state); err != nil {
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
	if err = posting.DeleteOwnPost(context.Background(), "post_1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.DeleteSecrets["post_1"] != "" {
		t.Fatalf("state=%#v err=%v", loaded, err)
	}
	if err = posting.DeleteOwnPost(context.Background(), "post_2"); err == nil {
		t.Fatal("missing delete capability accepted")
	}
}

func TestPostingAPIPersistsMentionsWithoutExposingDeleteSecret(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	state, err := NewState("https://board.example")
	if err != nil {
		t.Fatal(err)
	}
	state.DeleteSecrets["post_0001"] = "secret-value"
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	posting, err := NewPostingAPI(&API{}, store, &state)
	if err != nil {
		t.Fatal(err)
	}
	if !posting.OwnsPost("post_0001") || posting.OwnsPost("post_other") {
		t.Fatal("local ownership lookup failed")
	}
	mention := Mention{ThreadID: "thread_1", PostID: "post_0002", ReplyToID: "post_0001"}
	if err := posting.RememberMention(mention); err != nil {
		t.Fatal(err)
	}
	if err := posting.SetMentionsCollapsed(true); err != nil {
		t.Fatal(err)
	}
	mentions, collapsed := posting.InitialMentions()
	if len(mentions) != 1 || !collapsed {
		t.Fatalf("mentions=%+v collapsed=%v", mentions, collapsed)
	}
	if err := posting.DismissMention("post_0002"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || len(loaded.Mentions) != 0 || !loaded.MentionsCollapsed {
		t.Fatalf("loaded=%+v err=%v", loaded.Mentions, err)
	}
}

func mapCreateThread(body string) generated.CreateThread {
	return generated.CreateThread{Body: &body}
}
