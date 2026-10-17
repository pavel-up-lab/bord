package moderation

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrInvalidReport = errors.New("invalid report")
var ErrReportLimited = errors.New("report rate limited")

type ReportReason string

const (
	ReportSpam    ReportReason = "spam"
	ReportIllegal ReportReason = "illegal"
	ReportThreat  ReportReason = "threat"
	ReportOther   ReportReason = "other"
)

func (r ReportReason) Valid() bool {
	return r == ReportSpam || r == ReportIllegal || r == ReportThreat || r == ReportOther
}

type Report struct {
	ID        string
	PostID    string
	Reason    ReportReason
	Source    string
	CreatedAt time.Time
}

type ReportRepository interface {
	AddIfNew(context.Context, Report) (created bool, uniqueSources int, err error)
	List(context.Context) ([]Report, error)
}

type ReportRateLimiter interface {
	AllowReport(source string) (bool, time.Duration)
}

type ReportService struct {
	repository ReportRepository
	limiter    ReportRateLimiter
	clock      interface{ Now() time.Time }
}

func NewReportService(repository ReportRepository, limiter ReportRateLimiter, clock interface{ Now() time.Time }) *ReportService {
	return &ReportService{repository: repository, limiter: limiter, clock: clock}
}

func (s *ReportService) Submit(ctx context.Context, id, postID string, reason ReportReason, source string) (bool, int, error) {
	if id == "" || postID == "" || source == "" || !reason.Valid() {
		return false, 0, ErrInvalidReport
	}
	if s.limiter != nil {
		if allowed, _ := s.limiter.AllowReport(source); !allowed {
			return false, 0, ErrReportLimited
		}
	}
	return s.repository.AddIfNew(ctx, Report{ID: id, PostID: postID, Reason: reason, Source: source, CreatedAt: s.clock.Now()})
}

type MemoryReportRepository struct {
	mu      sync.RWMutex
	reports []Report
	seen    map[string]struct{}
}

func NewMemoryReportRepository() *MemoryReportRepository {
	return &MemoryReportRepository{seen: make(map[string]struct{})}
}

func (r *MemoryReportRepository) AddIfNew(_ context.Context, report Report) (bool, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := report.PostID + "\x00" + report.Source
	_, exists := r.seen[key]
	if !exists {
		r.seen[key] = struct{}{}
		r.reports = append(r.reports, report)
	}
	count := 0
	for _, current := range r.reports {
		if current.PostID == report.PostID {
			count++
		}
	}
	return !exists, count, nil
}

func (r *MemoryReportRepository) List(context.Context) ([]Report, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Report(nil), r.reports...), nil
}
