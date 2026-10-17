package security

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrInvalidChallenge = errors.New("invalid challenge")
var ErrSolverExhausted = errors.New("solver work limit exhausted")

type Challenge struct {
	ID         string `json:"id"`
	Purpose    string `json:"purpose"`
	Subject    string `json:"subject"`
	Difficulty uint8  `json:"difficulty"`
	ExpiresAt  int64  `json:"expires_at"`
	Nonce      string `json:"nonce"`
}

type ChallengeUseStore interface {
	Use(id string, expiresAt, now time.Time) bool
}

type MemoryChallengeUses struct {
	mu   sync.Mutex
	used map[string]time.Time
}

func NewMemoryChallengeUses() *MemoryChallengeUses {
	return &MemoryChallengeUses{used: make(map[string]time.Time)}
}

func (s *MemoryChallengeUses) Use(id string, expiresAt, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, expiry := range s.used {
		if !expiry.After(now) {
			delete(s.used, key)
		}
	}
	if _, exists := s.used[id]; exists {
		return false
	}
	s.used[id] = expiresAt
	return true
}

type PoW struct {
	key           []byte
	clock         Clock
	random        io.Reader
	uses          ChallengeUseStore
	maxDifficulty uint8
}

func NewPoW(key []byte, clock Clock, random io.Reader, uses ChallengeUseStore, maxDifficulty uint8) (*PoW, error) {
	if len(key) < 32 || maxDifficulty == 0 || maxDifficulty > 30 || uses == nil {
		return nil, errors.New("invalid PoW configuration")
	}
	return &PoW{key: key, clock: clock, random: random, uses: uses, maxDifficulty: maxDifficulty}, nil
}

func (p *PoW) Issue(purpose, subject string, difficulty uint8, ttl time.Duration) (string, error) {
	if purpose == "" || subject == "" || difficulty == 0 || difficulty > p.maxDifficulty || ttl <= 0 {
		return "", ErrInvalidChallenge
	}
	raw := make([]byte, 24)
	if _, err := io.ReadFull(p.random, raw); err != nil {
		return "", fmt.Errorf("generate challenge: %w", err)
	}
	b64 := base64.RawURLEncoding
	c := Challenge{ID: b64.EncodeToString(raw[:12]), Purpose: purpose, Subject: subject, Difficulty: difficulty, ExpiresAt: p.clock.Now().Add(ttl).Unix(), Nonce: b64.EncodeToString(raw[12:])}
	payload, _ := json.Marshal(c)
	signature := p.sign(payload)
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(signature), nil
}

func (p *PoW) Verify(token, purpose, subject string, solution uint64) error {
	c, payload, err := p.parse(token)
	if err != nil || c.Purpose != purpose || c.Subject != subject || c.Difficulty == 0 || c.Difficulty > p.maxDifficulty {
		return ErrInvalidChallenge
	}
	now := p.clock.Now()
	expires := time.Unix(c.ExpiresAt, 0)
	if !expires.After(now) || !validWork(payload, solution, c.Difficulty) {
		return ErrInvalidChallenge
	}
	if !p.uses.Use(c.ID, expires, now) {
		return ErrInvalidChallenge
	}
	return nil
}

func (p *PoW) parse(token string) (Challenge, []byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Challenge{}, nil, ErrInvalidChallenge
	}
	b64 := base64.RawURLEncoding
	payload, err := b64.DecodeString(parts[0])
	if err != nil {
		return Challenge{}, nil, ErrInvalidChallenge
	}
	signature, err := b64.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, p.sign(payload)) {
		return Challenge{}, nil, ErrInvalidChallenge
	}
	var c Challenge
	if err := json.Unmarshal(payload, &c); err != nil {
		return Challenge{}, nil, ErrInvalidChallenge
	}
	return c, payload, nil
}

func (p *PoW) sign(payload []byte) []byte {
	h := hmac.New(sha256.New, p.key)
	h.Write([]byte("bord/pow/v1\x00"))
	h.Write(payload)
	return h.Sum(nil)
}

func validWork(payload []byte, solution uint64, difficulty uint8) bool {
	h := sha256.New()
	h.Write([]byte("bord/pow-solution/v1\x00"))
	h.Write(payload)
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], solution)
	h.Write(raw[:])
	digest := h.Sum(nil)
	whole, rest := difficulty/8, difficulty%8
	for i := uint8(0); i < whole; i++ {
		if digest[i] != 0 {
			return false
		}
	}
	return rest == 0 || digest[whole]>>(8-rest) == 0
}

func Solve(ctx context.Context, token string, difficulty uint8, maxWork uint64) (uint64, error) {
	if difficulty == 0 || difficulty > 30 || maxWork == 0 {
		return 0, ErrSolverExhausted
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return 0, ErrInvalidChallenge
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, ErrInvalidChallenge
	}
	for solution := uint64(0); solution < maxWork; solution++ {
		if solution%1024 == 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			default:
			}
		}
		if validWork(payload, solution, difficulty) {
			return solution, nil
		}
	}
	return 0, ErrSolverExhausted
}

func ChallengeID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ""
	}
	var c Challenge
	if json.Unmarshal(payload, &c) != nil {
		return ""
	}
	return c.ID + ":" + strconv.FormatInt(c.ExpiresAt, 10)
}
