package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"bord/internal/api"
	"bord/internal/app/attachment"
	boardapp "bord/internal/app/board"
	"bord/internal/app/moderation"
	"bord/internal/domain"
	"bord/internal/security"
	storage "bord/internal/storage/sqlite"
)

type Admin struct {
	db          *sql.DB
	repo        *storage.Repository
	clock       Clock
	auth        *security.AdminAuthenticator
	keyring     security.IPKeyring
	lookupKey   []byte
	attachments attachment.Service
}

func newAdmin(repo *storage.Repository, clock Clock, keyring security.IPKeyring, lookupKey []byte, attachments attachment.Service) (*Admin, error) {
	dummy, err := security.HashSecret([]byte("dummy-administrator-secret"), security.DefaultArgon2Params, rand.Reader)
	if err != nil {
		return nil, err
	}
	db := repo.DB()
	identities := adminIdentities{db: db}
	return &Admin{db: db, repo: repo, clock: clock, auth: security.NewAdminAuthenticator(identities, clock, dummy), keyring: keyring, lookupKey: append([]byte(nil), lookupKey...), attachments: attachments}, nil
}

func (a *Admin) Bootstrap(ctx context.Context, id string, role security.Role, expires time.Time) (string, error) {
	credential, identity, err := security.NewAdminCredential(id, role, expires, rand.Reader, security.DefaultArgon2Params)
	if err != nil {
		return "", err
	}
	return credential, adminIdentities{db: a.db}.Save(ctx, identity)
}

func (a *Admin) Authenticate(ctx context.Context, bearer, requestID string) (api.AdminPrincipal, error) {
	identity, err := a.auth.Authenticate(ctx, bearer, security.RoleReports)
	auditErr := a.audit(ctx, identity.ID, "admin.authenticate", "", "", requestID, err)
	if err != nil {
		return api.AdminPrincipal{}, errors.Join(mapAdminError(err), auditErr)
	}
	if auditErr != nil {
		return api.AdminPrincipal{}, auditErr
	}
	return api.AdminPrincipal{ID: identity.ID, Role: identity.Role, ExpiresAt: identity.ExpiresAt}, nil
}

func (a *Admin) actor(ctx context.Context, required security.Role) (api.AdminPrincipal, error) {
	actor, ok := api.AdminPrincipalFromContext(ctx)
	if !ok {
		return actor, domain.NewError(domain.CodeUnauthorized, "", nil)
	}
	if !actor.Role.Allows(required) {
		return actor, domain.NewError(domain.CodeForbidden, "", nil)
	}
	return actor, nil
}

var boardSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func (a *Admin) ListBoards(ctx context.Context, state, cursor string, limit int) (api.Page[api.AdminBoard], error) {
	if _, err := a.actor(ctx, security.RoleAdministrator); err != nil {
		return api.Page[api.AdminBoard]{}, err
	}
	if state == "" {
		state = string(boardapp.BoardStateAll)
	}
	var before domain.BoardID
	if cursor != "" {
		if _, err := strconv.ParseInt(cursor, 10, 64); err != nil {
			return api.Page[api.AdminBoard]{}, domain.NewError(domain.CodeValidation, "cursor", err)
		}
		before = domain.BoardID(cursor)
	}
	page, err := a.repo.ListManagedBoards(ctx, boardapp.BoardState(state), before, limit)
	if err != nil {
		return api.Page[api.AdminBoard]{}, err
	}
	result := api.Page[api.AdminBoard]{Items: make([]api.AdminBoard, 0, len(page.Boards))}
	for _, item := range page.Boards {
		result.Items = append(result.Items, managedBoardDTO(item))
	}
	result.NextCursor = string(page.Next)
	return result, nil
}

func (a *Admin) CreateBoard(ctx context.Context, request api.AdminCreateBoardRequest, requestID string) (api.AdminBoard, error) {
	actor, err := a.actor(ctx, security.RoleAdministrator)
	target := request.Slug
	if err == nil {
		err = validateBoardMutation(request.Slug, request.Title, request.Reason)
	}
	if err != nil {
		return api.AdminBoard{}, errors.Join(err, a.audit(ctx, actor.ID, "board.create", target, request.Reason, requestID, err))
	}
	var created api.AdminBoard
	err = a.mutateWithAudit(ctx, actor.ID, "board.create", &target, request.Reason, requestID, func(tx *sql.Tx) error {
		var numericID int64
		if scanErr := tx.QueryRowContext(ctx, `INSERT INTO id_sequences(kind,value) VALUES('board',1) ON CONFLICT(kind) DO UPDATE SET value=value+1 RETURNING value`).Scan(&numericID); scanErr != nil {
			return scanErr
		}
		board, domainErr := domain.NewBoard(domain.BoardID(fmtID(numericID)), request.Slug, request.Title)
		if domainErr != nil {
			return domainErr
		}
		_, insertErr := tx.ExecContext(ctx, `INSERT INTO boards(id,slug,title,enabled,created_at) VALUES(?,?,?,?,?)`, numericID, board.Slug, board.Title, 1, encodeTime(a.clock.Now()))
		if insertErr != nil {
			if strings.Contains(strings.ToLower(insertErr.Error()), "constraint") {
				return domain.NewError(domain.CodeConflict, "slug", insertErr)
			}
			return insertErr
		}
		target = fmtID(numericID)
		created = api.AdminBoard{ID: target, Slug: board.Slug, Title: board.Title, State: "active"}
		return nil
	})
	return created, err
}

func (a *Admin) ArchiveBoard(ctx context.Context, id, reason, requestID string) (api.AdminBoard, error) {
	return a.setBoardState(ctx, id, reason, requestID, true, false, "board.archive")
}

func (a *Admin) RestoreBoard(ctx context.Context, id, reason, requestID string) (api.AdminBoard, error) {
	return a.setBoardState(ctx, id, reason, requestID, false, true, "board.restore")
}

func (a *Admin) setBoardState(ctx context.Context, id, reason, requestID string, expected, enabled bool, action string) (api.AdminBoard, error) {
	actor, err := a.actor(ctx, security.RoleAdministrator)
	if err == nil {
		if _, parseErr := parseAdminID(id); parseErr != nil {
			err = domain.NewError(domain.CodeBoardNotFound, "board_id", parseErr)
		} else if !validAdminReason(reason) {
			err = domain.NewError(domain.CodeValidation, "reason", nil)
		}
	}
	if err != nil {
		return api.AdminBoard{}, errors.Join(err, a.audit(ctx, actor.ID, action, id, reason, requestID, err))
	}
	var result api.AdminBoard
	err = a.mutateWithAudit(ctx, actor.ID, action, &id, reason, requestID, func(tx *sql.Tx) error {
		numericID, _ := parseAdminID(id)
		toInt := func(value bool) int {
			if value {
				return 1
			}
			return 0
		}
		updated, updateErr := tx.ExecContext(ctx, `UPDATE boards SET enabled=? WHERE id=? AND enabled=?`, toInt(enabled), numericID, toInt(expected))
		if updateErr != nil {
			return updateErr
		}
		if changed, _ := updated.RowsAffected(); changed != 1 {
			var exists int
			lookupErr := tx.QueryRowContext(ctx, `SELECT count(*) FROM boards WHERE id=?`, numericID).Scan(&exists)
			if lookupErr != nil {
				return lookupErr
			}
			if exists == 0 {
				return domain.NewError(domain.CodeBoardNotFound, "board_id", nil)
			}
			return domain.NewError(domain.CodeConflict, "board", nil)
		}
		return scanAdminBoard(ctx, tx, numericID, &result)
	})
	return result, err
}

func validateBoardMutation(slug, title, reason string) error {
	if !boardSlugPattern.MatchString(slug) {
		return domain.NewError(domain.CodeValidation, "slug", nil)
	}
	if strings.TrimSpace(title) != title || len(title) < 1 || len(title) > 100 {
		return domain.NewError(domain.CodeValidation, "title", nil)
	}
	if !validAdminReason(reason) {
		return domain.NewError(domain.CodeValidation, "reason", nil)
	}
	return nil
}

func validAdminReason(reason string) bool {
	return strings.TrimSpace(reason) == reason && len(reason) >= 3 && len(reason) <= 500 && !security.ContainsRawIP(reason)
}

func parseAdminID(id string) (int64, error) {
	numeric, err := strconv.ParseInt(id, 10, 64)
	if err != nil || numeric < 1 {
		return 0, fmt.Errorf("invalid resource ID")
	}
	return numeric, nil
}

func scanAdminBoard(ctx context.Context, row interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64, target *api.AdminBoard) error {
	var enabled int
	err := row.QueryRowContext(ctx, `SELECT b.id,b.slug,b.title,b.enabled,(SELECT count(*) FROM threads t WHERE t.board_id=b.id),(SELECT count(*) FROM posts p JOIN threads t ON t.id=p.thread_id WHERE t.board_id=b.id) FROM boards b WHERE b.id=?`, id).
		Scan(&id, &target.Slug, &target.Title, &enabled, &target.ThreadCount, &target.PostCount)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NewError(domain.CodeBoardNotFound, "board_id", err)
	}
	if err != nil {
		return err
	}
	target.ID = fmtID(id)
	target.State = "archived"
	if enabled == 1 {
		target.State = "active"
	}
	return nil
}

func managedBoardDTO(item boardapp.ManagedBoard) api.AdminBoard {
	state := "archived"
	if item.Board.Enabled {
		state = "active"
	}
	return api.AdminBoard{ID: string(item.Board.ID), Slug: item.Board.Slug, Title: item.Board.Title, State: state, ThreadCount: item.ThreadCount, PostCount: item.PostCount}
}

func (a *Admin) ListReports(ctx context.Context, cursor string, limit int) (api.Page[api.AdminReport], error) {
	after := int64(0)
	if cursor != "" {
		var err error
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return api.Page[api.AdminReport]{}, domain.NewError(domain.CodeValidation, "cursor", err)
		}
	}
	rows, err := a.db.QueryContext(ctx, `SELECT id,post_id,reason,created_at FROM reports WHERE id>? ORDER BY id LIMIT ?`, after, limit+1)
	if err != nil {
		return api.Page[api.AdminReport]{}, err
	}
	defer rows.Close()
	out := api.Page[api.AdminReport]{Items: []api.AdminReport{}}
	for rows.Next() {
		var x api.AdminReport
		var reportID, postID int64
		var at string
		if err = rows.Scan(&reportID, &postID, &x.Reason, &at); err != nil {
			return out, err
		}
		x.ID, x.PostID = fmtID(reportID), fmtID(postID)
		x.CreatedAt, _ = time.Parse(time.RFC3339Nano, at)
		if len(out.Items) == limit {
			out.NextCursor = out.Items[len(out.Items)-1].ID
			break
		}
		out.Items = append(out.Items, x)
	}
	return out, rows.Err()
}

func (a *Admin) GetPostContext(ctx context.Context, postID, cursor string, limit int) (api.AdminPostContext, error) {
	if _, err := a.actor(ctx, security.RoleModerator); err != nil {
		return api.AdminPostContext{}, err
	}
	var targetID, threadID, boardID int64
	var threadState, slug, title string
	err := a.db.QueryRowContext(ctx, `
		SELECT p.id,p.thread_id,t.board_id,t.status,b.slug,b.title
		FROM posts p JOIN threads t ON t.id=p.thread_id JOIN boards b ON b.id=t.board_id
		WHERE p.id=?`, postID).Scan(&targetID, &threadID, &boardID, &threadState, &slug, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return api.AdminPostContext{}, domain.NewError(domain.CodePostNotFound, "post_id", err)
	}
	if err != nil {
		return api.AdminPostContext{}, err
	}
	after := int64(0)
	if cursor != "" {
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 0 {
			return api.AdminPostContext{}, domain.NewError(domain.CodeValidation, "cursor", err)
		}
	} else if targetID > int64(limit/2) {
		after = targetID - int64(limit/2) - 1
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT p.id,p.thread_id,p.reply_to_id,p.author_id,p.status,
		       CASE WHEN p.status='hidden' THEN coalesce(p.hidden_body,'') ELSE p.body END,
		       p.created_at,coalesce(p.deleted_at,''),
		       a.id,a.media_type,a.size_bytes,a.sha256,a.width,a.height,a.safe_filename
		FROM posts p
		LEFT JOIN attachments a ON a.id=CASE WHEN p.status='hidden' THEN p.hidden_attachment_id ELSE p.attachment_id END
		WHERE p.thread_id=? AND p.id>? ORDER BY p.id LIMIT ?`, threadID, after, limit+1)
	if err != nil {
		return api.AdminPostContext{}, err
	}
	defer rows.Close()
	result := api.AdminPostContext{
		Board:      api.AdminBoardSummary{ID: fmtID(boardID), Slug: slug, Title: title},
		Thread:     api.AdminThreadSummary{ID: fmtID(threadID), Closed: threadState != "open"},
		TargetPost: fmtID(targetID),
		Posts:      []api.Post{},
	}
	for rows.Next() {
		post, numericID, scanErr := scanAdminContextPost(rows)
		if scanErr != nil {
			return result, scanErr
		}
		if len(result.Posts) == limit {
			result.NextCursor = strconv.FormatInt(numericID-1, 10)
			break
		}
		result.Posts = append(result.Posts, post)
	}
	return result, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanAdminContextPost(row rowScanner) (api.Post, int64, error) {
	var id, threadID int64
	var replyID sql.NullInt64
	var authorID, state, body, created, deleted string
	var attachmentID, mediaType, filename sql.NullString
	var size, width, height sql.NullInt64
	var hash []byte
	if err := row.Scan(&id, &threadID, &replyID, &authorID, &state, &body, &created, &deleted, &attachmentID, &mediaType, &size, &hash, &width, &height, &filename); err != nil {
		return api.Post{}, 0, err
	}
	post := api.Post{ID: fmtID(id), ThreadID: fmtID(threadID), AuthorID: authorID, State: state}
	post.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if state != "deleted" {
		post.Body = &body
	}
	if replyID.Valid {
		value := fmtID(replyID.Int64)
		post.ReplyToID = &value
	}
	if deleted != "" {
		value, _ := time.Parse(time.RFC3339Nano, deleted)
		post.DeletedAt = &value
	}
	if attachmentID.Valid {
		post.Attachment = &api.Attachment{ID: attachmentID.String, MediaType: mediaType.String, Filename: filename.String, Size: size.Int64, Width: int(width.Int64), Height: int(height.Int64), SHA256: hex.EncodeToString(hash)}
	}
	return post, id, nil
}

func (a *Admin) OpenAttachment(ctx context.Context, id string) (io.ReadCloser, api.Attachment, error) {
	if _, err := a.actor(ctx, security.RoleModerator); err != nil {
		return nil, api.Attachment{}, err
	}
	download, err := a.attachments.FetchForModeration(ctx, domain.AttachmentID(id))
	if err != nil {
		return nil, api.Attachment{}, mapAttachmentError(err)
	}
	return download.Body, attachmentDTO(download.Record), nil
}

func (a *Admin) ListBans(ctx context.Context, state, cursor string, limit int) (api.Page[api.AdminBan], error) {
	if _, err := a.actor(ctx, security.RoleModerator); err != nil {
		return api.Page[api.AdminBan]{}, err
	}
	before := int64(math.MaxInt64)
	if cursor != "" {
		parsed, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || parsed <= 0 {
			return api.Page[api.AdminBan]{}, domain.NewError(domain.CodeValidation, "cursor", err)
		}
		before = parsed
	}
	query := `SELECT id,subject_kind,subject_value,coalesce(operation,''),reason,created_at,expires_at,revoked_at FROM bans WHERE id<?`
	now := encodeTime(a.clock.Now())
	switch state {
	case "active":
		query += ` AND revoked_at IS NULL AND expires_at>?`
	case "history":
		query += ` AND (revoked_at IS NOT NULL OR expires_at<=?)`
	case "all":
	default:
		return api.Page[api.AdminBan]{}, domain.NewError(domain.CodeValidation, "state", nil)
	}
	args := []any{before}
	if state != "all" {
		args = append(args, now)
	}
	args = append(args, limit+1)
	query += ` ORDER BY id DESC LIMIT ?`
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return api.Page[api.AdminBan]{}, err
	}
	defer rows.Close()
	page := api.Page[api.AdminBan]{Items: []api.AdminBan{}}
	var lastID int64
	for rows.Next() {
		var id int64
		var subject []byte
		var created, expires string
		var revoked sql.NullString
		var item api.AdminBan
		if err := rows.Scan(&id, &item.SubjectType, &subject, &item.Operation, &item.Reason, &created, &expires, &revoked); err != nil {
			return page, err
		}
		if len(page.Items) == limit {
			page.NextCursor = strconv.FormatInt(lastID, 10)
			break
		}
		fingerprint := sha256.Sum256(subject)
		item.ID = fmtID(id)
		item.Subject = "fingerprint:" + hex.EncodeToString(fingerprint[:8])
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		item.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
		item.State = "active"
		if revoked.Valid {
			value, _ := time.Parse(time.RFC3339Nano, revoked.String)
			item.RevokedAt = &value
			item.State = "revoked"
		} else if !item.ExpiresAt.After(a.clock.Now()) {
			item.State = "expired"
		}
		page.Items = append(page.Items, item)
		lastID = id
	}
	return page, rows.Err()
}

func (a *Admin) ListAudit(ctx context.Context, cursor string, limit int) (api.Page[api.AdminAuditEntry], error) {
	if _, err := a.actor(ctx, security.RoleModerator); err != nil {
		return api.Page[api.AdminAuditEntry]{}, err
	}
	before := int64(math.MaxInt64)
	if cursor != "" {
		parsed, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || parsed <= 0 {
			return api.Page[api.AdminAuditEntry]{}, domain.NewError(domain.CodeValidation, "cursor", err)
		}
		before = parsed
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT id,coalesce(admin_id,''),action,coalesce(target_id,''),coalesce(reason,''),outcome,
		       coalesce(json_extract(payload,'$.error_code'),''),request_id,created_at
		FROM moderation_actions WHERE id<? ORDER BY id DESC LIMIT ?`, before, limit+1)
	if err != nil {
		return api.Page[api.AdminAuditEntry]{}, err
	}
	defer rows.Close()
	page := api.Page[api.AdminAuditEntry]{Items: []api.AdminAuditEntry{}}
	var lastID int64
	for rows.Next() {
		var id int64
		var created string
		var item api.AdminAuditEntry
		if err := rows.Scan(&id, &item.ActorID, &item.Action, &item.TargetID, &item.Reason, &item.Outcome, &item.ErrorCode, &item.RequestID, &created); err != nil {
			return page, err
		}
		if len(page.Items) == limit {
			page.NextCursor = strconv.FormatInt(lastID, 10)
			break
		}
		item.ID = fmtID(id)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if security.ContainsRawIP(item.Reason) {
			item.Reason = "[redacted: raw IP address]"
		}
		page.Items = append(page.Items, item)
		lastID = id
	}
	return page, rows.Err()
}
func (a *Admin) SetPostVisibility(ctx context.Context, id string, visible bool, reason, requestID string) error {
	actor, err := a.actor(ctx, security.RoleModerator)
	if err != nil {
		return errors.Join(err, a.audit(ctx, actor.ID, "post.visibility", id, reason, requestID, err))
	}
	err = a.mutateWithAudit(ctx, actor.ID, "post.visibility", &id, reason, requestID, func(tx *sql.Tx) error {
		query := `UPDATE posts SET hidden_body=body,hidden_attachment_id=attachment_id,body='',attachment_id=NULL,status='hidden' WHERE id=? AND status='visible'`
		if visible {
			query = `UPDATE posts SET body=coalesce(hidden_body,''),attachment_id=hidden_attachment_id,hidden_body=NULL,hidden_attachment_id=NULL,status='visible' WHERE id=? AND status='hidden'`
		}
		res, actionErr := tx.ExecContext(ctx, query, id)
		if actionErr == nil {
			if n, _ := res.RowsAffected(); n != 1 {
				actionErr = domain.NewError(domain.CodePostNotFound, "post_id", nil)
			}
		}
		if actionErr == nil {
			var boardID, threadID int64
			actionErr = tx.QueryRowContext(ctx, `SELECT t.board_id,p.thread_id FROM posts p JOIN threads t ON t.id=p.thread_id WHERE p.id=?`, id).Scan(&boardID, &threadID)
			if actionErr == nil {
				eventType := "post.deleted"
				if visible {
					eventType = "post.updated"
				}
				actionErr = appendAdminEvent(ctx, tx, eventType, boardID, threadID, id, a.clock.Now())
			}
		}
		return actionErr
	})
	return err
}
func (a *Admin) CloseThread(ctx context.Context, id, reason, requestID string) error {
	actor, err := a.actor(ctx, security.RoleModerator)
	if err != nil {
		return errors.Join(err, a.audit(ctx, actor.ID, "thread.close", id, reason, requestID, err))
	}
	return a.mutateWithAudit(ctx, actor.ID, "thread.close", &id, reason, requestID, func(tx *sql.Tx) error {
		res, actionErr := tx.ExecContext(ctx, `UPDATE threads SET status='closed' WHERE id=?`, id)
		if actionErr == nil {
			if n, _ := res.RowsAffected(); n != 1 {
				actionErr = domain.NewError(domain.CodeThreadNotFound, "thread_id", nil)
			}
		}
		if actionErr == nil {
			var boardID int64
			actionErr = tx.QueryRowContext(ctx, `SELECT board_id FROM threads WHERE id=?`, id).Scan(&boardID)
			if actionErr == nil {
				actionErr = appendAdminEvent(ctx, tx, "thread.closed", boardID, id, nil, a.clock.Now())
			}
		}
		return actionErr
	})
}
func (a *Admin) CreateBan(ctx context.Context, b api.BanRequest, requestID string) (api.Ban, error) {
	actor, err := a.actor(ctx, security.RoleModerator)
	var id int64
	if err != nil {
		return api.Ban{}, errors.Join(err, a.audit(ctx, actor.ID, "ban.create", "", b.Reason, requestID, err))
	}
	if !b.ExpiresAt.After(a.clock.Now()) || len(b.Operations) < 1 || len(b.Operations) > 10 {
		err = domain.NewError(domain.CodeValidation, "expires_at", nil)
	}
	subjectKind, subjectValue := b.SubjectType, []byte(b.Subject)
	switch b.SubjectType {
	case "ip":
		address, parseErr := netip.ParseAddr(b.Subject)
		if parseErr != nil {
			err = domain.NewError(domain.CodeValidation, "subject", parseErr)
		} else if lookup, lookupErr := security.IPLookup(a.lookupKey, address); lookupErr != nil {
			err = lookupErr
		} else {
			subjectKind, subjectValue = "lookup", lookup[:]
		}
	case "prefix":
		prefix, parseErr := netip.ParsePrefix(b.Subject)
		if parseErr != nil {
			err = domain.NewError(domain.CodeValidation, "subject", parseErr)
		} else {
			subjectValue = []byte(prefix.Masked().String())
		}
	case "lookup":
		decoded, decodeErr := hex.DecodeString(b.Subject)
		if decodeErr != nil || len(decoded) != 32 {
			err = domain.NewError(domain.CodeValidation, "subject", decodeErr)
		} else {
			subjectValue = decoded
		}
	}
	if b.Board != nil {
		// The frozen abuse port does not carry a board dimension. Refuse to
		// silently widen a board-specific ban to every board.
		err = domain.NewError(domain.CodeValidation, "board", errors.New("board-scoped bans are not supported by this runtime"))
	}
	allowedOperations := map[string]bool{"create_thread": true, "create_post": true, "delete_post": true, "report": true}
	for _, operation := range b.Operations {
		if !allowedOperations[operation] {
			err = domain.NewError(domain.CodeValidation, "operations", nil)
		}
	}
	if err != nil {
		return api.Ban{}, errors.Join(err, a.audit(ctx, actor.ID, "ban.create", "", b.Reason, requestID, err))
	}
	auditTarget := ""
	err = a.mutateWithAudit(ctx, actor.ID, "ban.create", &auditTarget, b.Reason, requestID, func(tx *sql.Tx) error {
		for _, op := range b.Operations {
			res, actionErr := tx.ExecContext(ctx, `INSERT INTO bans(subject_kind,subject_value,operation,reason,created_at,expires_at) VALUES(?,?,?,?,?,?)`, subjectKind, subjectValue, op, b.Reason, encodeTime(a.clock.Now()), encodeTime(b.ExpiresAt))
			if actionErr != nil {
				return actionErr
			}
			if id == 0 {
				id, _ = res.LastInsertId()
				auditTarget = strconv.FormatInt(id, 10)
			}
		}
		return nil
	})
	if err != nil {
		return api.Ban{}, err
	}
	return api.Ban{ID: fmt.Sprintf("%08d", id), ExpiresAt: b.ExpiresAt}, nil
}

func (a *Admin) CreatePostBan(ctx context.Context, postID string, request api.PostBanRequest, requestID string) (api.Ban, error) {
	actor, authErr := a.actor(ctx, security.RoleModerator)
	if authErr != nil {
		return api.Ban{}, errors.Join(authErr, a.audit(ctx, actor.ID, "ban.create", postID, request.Reason, requestID, authErr))
	}
	if !request.ExpiresAt.After(a.clock.Now()) || !validPostBanOperations(request.Operations) {
		err := domain.NewError(domain.CodeValidation, "operations", nil)
		return api.Ban{}, errors.Join(err, a.audit(ctx, actor.ID, "ban.create", postID, request.Reason, requestID, err))
	}
	var lookup []byte
	err := a.db.QueryRowContext(ctx, `SELECT ip_lookup FROM request_subjects WHERE post_id=? ORDER BY id DESC LIMIT 1`, postID).Scan(&lookup)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.NewError(domain.CodePostNotFound, "post_id", err)
	}
	if err != nil {
		return api.Ban{}, errors.Join(err, a.audit(ctx, actor.ID, "ban.create", postID, request.Reason, requestID, err))
	}
	return a.CreateBan(ctx, api.BanRequest{
		SubjectType: "lookup",
		Subject:     hex.EncodeToString(lookup),
		Operations:  append([]string(nil), request.Operations...),
		ExpiresAt:   request.ExpiresAt,
		Reason:      request.Reason,
	}, requestID)
}

func validPostBanOperations(operations []string) bool {
	if len(operations) < 1 || len(operations) > 4 {
		return false
	}
	allowed := map[string]bool{"create_thread": true, "create_post": true, "delete_post": true, "report": true}
	seen := map[string]bool{}
	for _, operation := range operations {
		if !allowed[operation] || seen[operation] {
			return false
		}
		seen[operation] = true
	}
	return true
}
func (a *Admin) RevokeBan(ctx context.Context, id, reason, requestID string) error {
	actor, err := a.actor(ctx, security.RoleModerator)
	if err != nil {
		return errors.Join(err, a.audit(ctx, actor.ID, "ban.revoke", id, reason, requestID, err))
	}
	return a.mutateWithAudit(ctx, actor.ID, "ban.revoke", &id, reason, requestID, func(tx *sql.Tx) error {
		result, actionErr := tx.ExecContext(ctx, `UPDATE bans SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, encodeTime(a.clock.Now()), id)
		if actionErr == nil {
			if changed, _ := result.RowsAffected(); changed != 1 {
				actionErr = domain.NewError(domain.CodeValidation, "ban_id", nil)
			}
		}
		return actionErr
	})
}
func (a *Admin) RevealPostIP(ctx context.Context, postID, reason, requestID string) (string, error) {
	actor, err := a.actor(ctx, security.RoleAdministrator)
	var result string
	if err == nil {
		var env security.IPEnvelope
		env.Version = 1
		err = a.db.QueryRowContext(ctx, `SELECT key_id,nonce,encrypted_ip FROM request_subjects WHERE post_id=? AND encrypted_ip IS NOT NULL ORDER BY id DESC LIMIT 1`, postID).Scan(&env.KeyID, &env.Nonce, &env.Ciphertext)
		if errors.Is(err, sql.ErrNoRows) {
			err = domain.NewError(domain.CodePostNotFound, "post_id", err)
		}
		if err == nil {
			var ip netip.Addr
			ip, err = a.keyring.Decrypt(env)
			result = ip.String()
		}
	}
	auditErr := a.audit(ctx, actor.ID, "ip.reveal", postID, reason, requestID, err)
	if err != nil || auditErr != nil {
		return "", errors.Join(err, auditErr)
	}
	return result, nil
}

func (a *Admin) audit(ctx context.Context, actor, action, target, reason, requestID string, actionErr error) error {
	return a.auditWith(ctx, a.db, actor, action, target, reason, requestID, actionErr)
}

type auditExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (a *Admin) auditWith(ctx context.Context, executor auditExecutor, actor, action, target, reason, requestID string, actionErr error) error {
	// API validation normally denies raw addresses. Keep the persistence adapter
	// safe for direct/internal callers too: audit availability must not become a
	// privacy leak, nor should an unsafe reason erase the attempted action.
	if security.ContainsRawIP(reason) {
		reason = "[redacted: raw IP address]"
	}
	entry := moderation.AuditEntry{ActorID: actor, Action: action, TargetID: target, Reason: reason, RequestID: requestID, Success: actionErr == nil, CreatedAt: a.clock.Now()}
	if actionErr != nil {
		entry.ErrorCode = string(domain.ErrorCode(actionErr))
	}
	if err := moderation.ValidateAudit(entry); err != nil {
		return err
	}
	outcome := "success"
	payload := []byte(`{}`)
	if actionErr != nil {
		outcome = "failure"
		payload, _ = json.Marshal(map[string]string{"error_code": entry.ErrorCode})
	}
	_, err := executor.ExecContext(ctx, `INSERT INTO moderation_actions(admin_id,action,target_id,reason,outcome,request_id,payload,created_at) VALUES(NULLIF(?,''),?,?,?,?,?,?,?)`, actor, action, target, reason, outcome, requestID, payload, encodeTime(entry.CreatedAt))
	return err
}

// mutateWithAudit commits a successful moderation mutation only together with
// its audit record. Failed attempts are rolled back and then audited as a
// separate append-only record.
func (a *Admin) mutateWithAudit(ctx context.Context, actor, action string, target *string, reason, requestID string, mutate func(*sql.Tx) error) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Join(err, a.audit(ctx, actor, action, *target, reason, requestID, err))
	}
	actionErr := mutate(tx)
	if actionErr != nil {
		rollbackErr := tx.Rollback()
		if errors.Is(rollbackErr, sql.ErrTxDone) {
			rollbackErr = nil
		}
		return errors.Join(actionErr, rollbackErr, a.audit(ctx, actor, action, *target, reason, requestID, actionErr))
	}
	if err := a.auditWith(ctx, tx, actor, action, *target, reason, requestID, nil); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func appendAdminEvent(ctx context.Context, tx *sql.Tx, eventType string, boardID int64, threadID, postID any, at time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO event_outbox(schema_version,event_type,board_id,thread_id,post_id,payload,created_at) VALUES(1,?,?,?,?,?,?)`, eventType, boardID, threadID, postID, []byte(`{}`), encodeTime(at))
	return err
}
func mapAdminError(err error) error {
	if errors.Is(err, security.ErrUnauthorized) {
		return domain.NewError(domain.CodeUnauthorized, "", err)
	}
	if errors.Is(err, security.ErrRoleDenied) {
		return domain.NewError(domain.CodeForbidden, "", err)
	}
	return err
}

type adminIdentities struct{ db *sql.DB }

func (r adminIdentities) ByID(ctx context.Context, id string) (security.AdminIdentity, bool, error) {
	var x security.AdminIdentity
	var role string
	var verifier []byte
	var expiry string
	var revoked sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT id,role,credential_verifier,coalesce(expires_at,''),revoked_at FROM admin_identities WHERE id=?`, id).Scan(&x.ID, &role, &verifier, &expiry, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return x, false, nil
	}
	if err != nil {
		return x, false, err
	}
	x.Role = parseRole(role)
	x.Verifier = string(verifier)
	x.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiry)
	if revoked.Valid {
		v, _ := time.Parse(time.RFC3339Nano, revoked.String)
		x.RevokedAt = &v
	}
	return x, true, nil
}
func (r adminIdentities) Save(ctx context.Context, x security.AdminIdentity) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO admin_identities(id,role,credential_verifier,created_at,expires_at,revoked_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET role=excluded.role,credential_verifier=excluded.credential_verifier,expires_at=excluded.expires_at,revoked_at=excluded.revoked_at`, x.ID, roleName(x.Role), []byte(x.Verifier), encodeTime(time.Now()), encodeTime(x.ExpiresAt), nil)
	return err
}
func roleName(r security.Role) string {
	switch r {
	case security.RoleReports:
		return "reports"
	case security.RoleModerator:
		return "moderator"
	case security.RoleAdministrator:
		return "administrator"
	}
	return "invalid"
}
func parseRole(v string) security.Role {
	switch v {
	case "reports":
		return security.RoleReports
	case "moderator":
		return security.RoleModerator
	case "administrator":
		return security.RoleAdministrator
	}
	return 0
}
