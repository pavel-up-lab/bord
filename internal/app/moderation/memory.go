package moderation

import (
	"context"
	"errors"
	"sync"
	"time"
)

type MemoryContentRepository struct {
	mu     sync.Mutex
	Hidden map[string]bool
	Closed map[string]bool
}

func NewMemoryContentRepository() *MemoryContentRepository {
	return &MemoryContentRepository{Hidden: make(map[string]bool), Closed: make(map[string]bool)}
}
func (r *MemoryContentRepository) SetPostHidden(_ context.Context, id string, hidden bool, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Hidden[id] = hidden
	return nil
}
func (r *MemoryContentRepository) SetThreadClosed(_ context.Context, id string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Closed[id] = true
	return nil
}

type MemoryBanRepository struct {
	mu    sync.RWMutex
	items map[string]BanRecord
}

func NewMemoryBanRepository() *MemoryBanRepository {
	return &MemoryBanRepository{items: make(map[string]BanRecord)}
}
func (r *MemoryBanRepository) Create(_ context.Context, ban BanRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[ban.ID]; ok {
		return errors.New("ban exists")
	}
	r.items[ban.ID] = ban
	return nil
}
func (r *MemoryBanRepository) Revoke(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ban, ok := r.items[id]
	if !ok {
		return errors.New("ban not found")
	}
	ban.RevokedAt = &at
	r.items[id] = ban
	return nil
}
func (r *MemoryBanRepository) List(context.Context) ([]BanRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]BanRecord, 0, len(r.items))
	for _, item := range r.items {
		result = append(result, item)
	}
	return result, nil
}

type MemoryIPSubjects struct {
	mu    sync.RWMutex
	Items map[string]EncryptedIP
}

func (r *MemoryIPSubjects) EnvelopeByID(_ context.Context, id string) (EncryptedIP, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.Items[id]
	if !ok {
		return EncryptedIP{}, errors.New("subject not found")
	}
	return item, nil
}

type RetentionIP struct {
	ID             string
	CreatedAt      time.Time
	LegalHold      bool
	ActiveBanUntil time.Time
}
type RetentionValue struct {
	ID        string
	ExpiresAt time.Time
}
type MemoryRetentionRepository struct {
	mu                      sync.Mutex
	IP                      []RetentionIP
	Idempotency, Challenges []RetentionValue
}

func (r *MemoryRetentionRepository) DeleteExpiredIP(_ context.Context, cutoff, now time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.IP[:0]
	var count int64
	for _, item := range r.IP {
		if !item.CreatedAt.After(cutoff) && !item.LegalHold && !item.ActiveBanUntil.After(now) {
			count++
		} else {
			kept = append(kept, item)
		}
	}
	r.IP = kept
	return count, nil
}
func (r *MemoryRetentionRepository) DeleteExpiredIdempotency(_ context.Context, cutoff time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept, count := filterRetention(r.Idempotency, cutoff)
	r.Idempotency = kept
	return count, nil
}
func (r *MemoryRetentionRepository) DeleteExpiredChallenges(_ context.Context, now time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept, count := filterRetention(r.Challenges, now)
	r.Challenges = kept
	return count, nil
}
func filterRetention(items []RetentionValue, boundary time.Time) ([]RetentionValue, int64) {
	kept := items[:0]
	var count int64
	for _, item := range items {
		if !item.ExpiresAt.After(boundary) {
			count++
		} else {
			kept = append(kept, item)
		}
	}
	return kept, count
}
