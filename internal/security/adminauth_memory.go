package security

import (
	"context"
	"sync"
)

type MemoryAdminIdentities struct {
	mu    sync.RWMutex
	items map[string]AdminIdentity
}

func NewMemoryAdminIdentities() *MemoryAdminIdentities {
	return &MemoryAdminIdentities{items: make(map[string]AdminIdentity)}
}

func (r *MemoryAdminIdentities) ByID(_ context.Context, id string) (AdminIdentity, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[id]
	return item, ok, nil
}

func (r *MemoryAdminIdentities) Save(_ context.Context, identity AdminIdentity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[identity.ID] = identity
	return nil
}
