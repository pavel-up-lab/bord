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
	rows, err := o.db.QueryContext(ctx, `SELECT e.id,e.schema_version,e.event_type,coalesce(e.board_id,0),coalesce(e.thread_id,0),coalesce(e.post_id,0),coalesce(p.reply_to_id,0) FROM event_outbox e LEFT JOIN posts p ON p.id=e.post_id WHERE e.id>? AND e.published_at IS NULL ORDER BY e.id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Event{}
	for rows.Next() {
		var e api.Event
		var board, thread, post, replyTo int64
		if err := rows.Scan(&e.ID, &e.Version, &e.Type, &board, &thread, &post, &replyTo); err != nil {
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
		if replyTo != 0 {
			e.ReplyToID = fmtID(replyTo)
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
