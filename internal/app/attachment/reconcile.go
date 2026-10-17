package attachment

import (
	"context"
	"errors"
	"time"
)

type CleanupRequest struct {
	Now        time.Time
	Grace      time.Duration
	Referenced map[string]struct{}
	DryRun     bool
}

type CleanupReport struct {
	TemporaryCandidates int
	OrphanCandidates    int
	Removed             int
	Bytes               int64
}

type Cleaner interface {
	Cleanup(context.Context, CleanupRequest) (CleanupReport, error)
}

type Reconciler struct {
	Repository Repository
	Cleaner    Cleaner
	Clock      Clock
	Grace      time.Duration
}

// Run snapshots database references before scanning the store. A blob that
// becomes referenced after the snapshot is protected by the grace period; the
// configured grace must exceed the maximum prepare/attach transaction window.
func (r Reconciler) Run(ctx context.Context, dryRun bool) (CleanupReport, error) {
	if r.Repository == nil || r.Cleaner == nil || r.Clock == nil || r.Grace < 0 {
		return CleanupReport{}, errors.New("attachment: invalid reconciler configuration")
	}
	references, err := r.Repository.ReferencedBlobKeys(ctx)
	if err != nil {
		return CleanupReport{}, err
	}
	return r.Cleaner.Cleanup(ctx, CleanupRequest{Now: r.Clock.Now(), Grace: r.Grace, Referenced: references, DryRun: dryRun})
}
