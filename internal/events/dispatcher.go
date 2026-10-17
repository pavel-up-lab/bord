package events

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"bord/internal/api"
	"bord/internal/domain"
)

type Outbox interface {
	Pending(context.Context, domain.EventID, int) ([]api.Event, error)
	MarkDelivered(context.Context, domain.EventID) error
}

type Dispatcher struct {
	Outbox   Outbox
	Hub      *Hub
	Logger   *slog.Logger
	Interval time.Duration
	Batch    int
}

func (d *Dispatcher) Run(ctx context.Context) error {
	if d.Outbox == nil || d.Hub == nil {
		return errors.New("outbox and hub are required")
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Interval <= 0 {
		d.Interval = 100 * time.Millisecond
	}
	if d.Batch <= 0 {
		d.Batch = 100
	}
	ticker := time.NewTicker(d.Interval)
	defer ticker.Stop()
	var after domain.EventID
	for {
		if err := d.dispatch(ctx, &after); err != nil && !errors.Is(err, context.Canceled) {
			d.Logger.ErrorContext(ctx, "outbox dispatch failed", "event", "outbox.dispatch_failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) dispatch(ctx context.Context, after *domain.EventID) error {
	events, err := d.Outbox.Pending(ctx, *after, d.Batch)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.ID <= *after {
			return errors.New("outbox returned non-monotonic event IDs")
		}
		if !d.Hub.Publish(event) {
			return errors.New("hub rejected event")
		}
		if err := d.Outbox.MarkDelivered(ctx, event.ID); err != nil {
			return err
		}
		*after = event.ID
	}
	return nil
}
