package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"

	"bord/api/generated"
	"bord/internal/security"
)

// PostingAPI adds locally-held anonymous credentials to create requests. It
// intentionally exposes no credential getters, and State's formatting methods
// redact all values if it is accidentally passed to a logger.
type PostingAPI struct {
	*API
	store *Store
	state *State
	mu    sync.Mutex
}

// NewPostingAPI binds an API to durable client state. The state pointer remains
// the source of truth so settings saved by the command cannot overwrite posting
// updates made while the TUI is running.
func NewPostingAPI(api *API, store *Store, state *State) (*PostingAPI, error) {
	if api == nil || store == nil || state == nil {
		return nil, errors.New("initialize anonymous posting")
	}
	p := &PostingAPI{API: api, store: store, state: state}
	if err := p.ensureState(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *PostingAPI) CreateThread(ctx context.Context, board, idempotencyKey string, body generated.CreateThread) (generated.CreateResult, error) {
	secret, proof, err := p.prepare("thread", board, idempotencyKey)
	if err != nil {
		return generated.CreateResult{}, err
	}
	verifier, err := deleteVerifier(secret)
	if err != nil {
		return generated.CreateResult{}, err
	}
	body.SessionProof, body.DeleteVerifier = &proof, &verifier
	result, err := p.API.CreateThread(ctx, board, idempotencyKey, body)
	if err != nil {
		return generated.CreateResult{}, err
	}
	if err := p.remember("thread", board, idempotencyKey, result.PostId, secret); err != nil {
		return generated.CreateResult{}, err
	}
	return result, nil
}

func (p *PostingAPI) CreatePost(ctx context.Context, threadID, idempotencyKey string, body generated.CreatePost) (generated.CreateResult, error) {
	secret, proof, err := p.prepare("post", threadID, idempotencyKey)
	if err != nil {
		return generated.CreateResult{}, err
	}
	verifier, err := deleteVerifier(secret)
	if err != nil {
		return generated.CreateResult{}, err
	}
	body.SessionProof, body.DeleteVerifier = &proof, &verifier
	result, err := p.API.CreatePost(ctx, threadID, idempotencyKey, body)
	if err != nil {
		return generated.CreateResult{}, err
	}
	if err := p.remember("post", threadID, idempotencyKey, result.PostId, secret); err != nil {
		return generated.CreateResult{}, err
	}
	return result, nil
}

func (p *PostingAPI) ensureState() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	candidate := cloneState(*p.state)
	changed := false
	if raw, err := base64.RawURLEncoding.DecodeString(candidate.SessionSecret); err != nil || len(raw) != 32 {
		fresh, err := NewState(candidate.Server)
		if err != nil {
			return err
		}
		candidate.SessionSecret = fresh.SessionSecret
		changed = true
	}
	if candidate.DeleteSecrets == nil {
		candidate.DeleteSecrets = map[string]string{}
		changed = true
	}
	if candidate.PendingDeleteSecrets == nil {
		candidate.PendingDeleteSecrets = map[string]string{}
		changed = true
	}
	if !changed {
		return nil
	}
	if err := p.store.Save(candidate); err != nil {
		return err
	}
	*p.state = candidate
	return nil
}

func (p *PostingAPI) prepare(kind, target, key string) (secret, proof string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	pendingKey := pendingCreateKey(kind, target, key)
	if secret = p.state.PendingDeleteSecrets[pendingKey]; secret != "" {
		return secret, p.state.SessionSecret, nil
	}
	secret, err = NewDeleteSecret()
	if err != nil {
		return "", "", err
	}
	candidate := cloneState(*p.state)
	candidate.PendingDeleteSecrets[pendingKey] = secret
	if err := p.store.Save(candidate); err != nil {
		return "", "", err
	}
	*p.state = candidate
	return secret, p.state.SessionSecret, nil
}

func (p *PostingAPI) remember(kind, target, key, postID, secret string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	candidate := cloneState(*p.state)
	candidate.DeleteSecrets[postID] = secret
	delete(candidate.PendingDeleteSecrets, pendingCreateKey(kind, target, key))
	if err := p.store.Save(candidate); err != nil {
		return err
	}
	*p.state = candidate
	return nil
}

func deleteVerifier(secret string) (string, error) {
	encoded, err := security.HashSecret([]byte(secret), security.DefaultArgon2Params, rand.Reader)
	if err != nil {
		return "", errors.New("prepare anonymous delete credential")
	}
	return base64.RawURLEncoding.EncodeToString([]byte(encoded)), nil
}

func pendingCreateKey(kind, target, key string) string {
	return kind + ":" + target + ":" + key
}

func cloneState(state State) State {
	state.DeleteSecrets = cloneStrings(state.DeleteSecrets)
	state.PendingDeleteSecrets = cloneStrings(state.PendingDeleteSecrets)
	return state
}

func cloneStrings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
