// Package moderation defines moderation use cases and storage integration ports.
package moderation

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

var ErrUnsafeAudit = errors.New("unsafe audit payload")

type AuditEntry struct {
	ID        string
	ActorID   string
	Action    string
	TargetID  string
	Reason    string
	Success   bool
	ErrorCode string
	RequestID string
	CreatedAt time.Time
	Metadata  map[string]string
}

type AuditRepository interface {
	Append(context.Context, AuditEntry) error
	List(context.Context) ([]AuditEntry, error)
}

func ValidateAudit(entry AuditEntry) error {
	if entry.Action == "" || entry.RequestID == "" || entry.CreatedAt.IsZero() {
		return ErrUnsafeAudit
	}
	for _, value := range []string{entry.ActorID, entry.Action, entry.TargetID, entry.Reason, entry.ErrorCode, entry.RequestID} {
		if containsRawIP(value) {
			return ErrUnsafeAudit
		}
	}
	for key, value := range entry.Metadata {
		normalized := strings.ToLower(key)
		for _, forbidden := range []string{"secret", "token", "credential", "bearer", "raw_ip", "plaintext", "ciphertext"} {
			if strings.Contains(normalized, forbidden) {
				return ErrUnsafeAudit
			}
		}
		if containsRawIP(key) || containsRawIP(value) {
			return ErrUnsafeAudit
		}
	}
	return nil
}

func containsRawIP(value string) bool {
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ',', ';', '=', '(', ')', '[', ']', '{', '}', '"', '\'':
			return true
		default:
			return false
		}
	}) {
		candidate := strings.Trim(field, ".")
		if _, err := netip.ParseAddr(candidate); err == nil {
			return true
		}
		if _, err := netip.ParsePrefix(candidate); err == nil {
			return true
		}
		if host, _, err := net.SplitHostPort(candidate); err == nil {
			if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
				return true
			}
		}
	}
	return false
}

// MemoryAuditRepository intentionally exposes no update/delete operation.
type MemoryAuditRepository struct {
	mu      sync.RWMutex
	entries []AuditEntry
}

func (r *MemoryAuditRepository) Append(_ context.Context, entry AuditEntry) error {
	if err := ValidateAudit(entry); err != nil {
		return err
	}
	entry.Metadata = cloneMap(entry.Metadata)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
	return nil
}

func (r *MemoryAuditRepository) List(context.Context) ([]AuditEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]AuditEntry, len(r.entries))
	copy(result, r.entries)
	for i := range result {
		result[i].Metadata = cloneMap(result[i].Metadata)
	}
	return result, nil
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
