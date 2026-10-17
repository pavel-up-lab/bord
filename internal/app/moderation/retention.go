package moderation

import (
	"context"
	"errors"
	"time"
)

const DefaultRetention = 90 * 24 * time.Hour

type RetentionRepository interface {
	DeleteExpiredIP(context.Context, time.Time, time.Time) (int64, error)
	DeleteExpiredIdempotency(context.Context, time.Time) (int64, error)
	DeleteExpiredChallenges(context.Context, time.Time) (int64, error)
}

type RetentionResult struct{ IP, Idempotency, Challenges int64 }

type RetentionJob struct {
	repository RetentionRepository
	clock      interface{ Now() time.Time }
	period     time.Duration
}

func NewRetentionJob(repository RetentionRepository, clock interface{ Now() time.Time }, period time.Duration) (*RetentionJob, error) {
	if period <= 0 {
		period = DefaultRetention
	}
	if repository == nil || clock == nil {
		return nil, errors.New("invalid retention configuration")
	}
	return &RetentionJob{repository: repository, clock: clock, period: period}, nil
}

func (j *RetentionJob) Run(ctx context.Context) (RetentionResult, error) {
	now := j.clock.Now()
	cutoff := now.Add(-j.period)
	var result RetentionResult
	var err error
	if result.IP, err = j.repository.DeleteExpiredIP(ctx, cutoff, now); err != nil {
		return result, err
	}
	if result.Idempotency, err = j.repository.DeleteExpiredIdempotency(ctx, cutoff); err != nil {
		return result, err
	}
	if result.Challenges, err = j.repository.DeleteExpiredChallenges(ctx, now); err != nil {
		return result, err
	}
	return result, nil
}
