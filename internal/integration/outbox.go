package integration

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"bord/internal/api"
	"bord/internal/domain"
)

type Outbox struct{ db *sql.DB }

func (o *Outbox) Pending(ctx context.Context, after domain.EventID, limit int) ([]api.Event, error) {
	rows, err := o.db.QueryContext(ctx, `SELECT id,schema_version,event_type,coalesce(board_id,0),coalesce(thread_id,0),coalesce(post_id,0) FROM event_outbox WHERE id>? AND published_at IS NULL ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Event{}
	for rows.Next() {
		var e api.Event
		var board, thread, post int64
		if err := rows.Scan(&e.ID, &e.Version, &e.Type, &board, &thread, &post); err != nil {
			return nil, err
		}
		if board != 0 {
			e.BoardID = fmtID(board)
		}
		if thread != 0 {
			e.ThreadID = fmtID(thread)
		}
		if post != 0 {
			e.PostID = fmtID(post)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (o *Outbox) MarkDelivered(ctx context.Context, id domain.EventID) error {
	res, err := o.db.ExecContext(ctx, `UPDATE event_outbox SET published_at=? WHERE id=? AND published_at IS NULL`, encodeTime(realClock{}.Now()), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("outbox event was not pending")
	}
	return nil
}
func fmtID(id int64) string {
	s := strconv.FormatInt(id, 10)
	for len(s) < 8 {
		s = "0" + s
	}
	return s
}
