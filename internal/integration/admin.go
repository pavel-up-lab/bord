package integration

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"bord/internal/api"
	"bord/internal/app/moderation"
	"bord/internal/domain"
	"bord/internal/security"
)

type Admin struct {
	db        *sql.DB
	clock     Clock
	auth      *security.AdminAuthenticator
	keyring   security.IPKeyring
	lookupKey []byte
	mu        sync.Mutex
	actors    map[string]security.AdminIdentity
}

func newAdmin(db *sql.DB, clock Clock, keyring security.IPKeyring, lookupKey []byte) (*Admin, error) {
	dummy, err := security.HashSecret([]byte("dummy-administrator-secret"), security.DefaultArgon2Params, rand.Reader)
	if err != nil {
		return nil, err
	}
	repo := adminIdentities{db: db}
	return &Admin{db: db, clock: clock, auth: security.NewAdminAuthenticator(repo, clock, dummy), keyring: keyring, lookupKey: append([]byte(nil), lookupKey...), actors: make(map[string]security.AdminIdentity)}, nil
}

func (a *Admin) Bootstrap(ctx context.Context, id string, role security.Role, expires time.Time) (string, error) {
	credential, identity, err := security.NewAdminCredential(id, role, expires, rand.Reader, security.DefaultArgon2Params)
	if err != nil {
		return "", err
	}
	return credential, adminIdentities{db: a.db}.Save(ctx, identity)
}

func (a *Admin) Authenticate(ctx context.Context, bearer, requestID string) error {
	identity, err := a.auth.Authenticate(ctx, bearer, security.RoleReports)
	auditErr := a.audit(ctx, identity.ID, "admin.authenticate", "", "", requestID, err)
	if err != nil {
		return errors.Join(mapAdminError(err), auditErr)
	}
	if auditErr != nil {
		return auditErr
	}
	a.mu.Lock()
	if _, collision := a.actors[requestID]; collision {
		a.mu.Unlock()
		return domain.NewError(domain.CodeForbidden, "", errors.New("duplicate active request ID"))
	}
	a.actors[requestID] = identity
	a.mu.Unlock()
	return nil
}

// Finish releases request-scoped authorization state even for read-only admin
// handlers and early handler failures.
func (a *Admin) Finish(requestID string) {
	a.mu.Lock()
	_, pending := a.actors[requestID]
	if pending {
		delete(a.actors, requestID)
	}
	a.mu.Unlock()
}
func (a *Admin) actor(requestID string, required security.Role) (security.AdminIdentity, error) {
	a.mu.Lock()
	actor, ok := a.actors[requestID]
	delete(a.actors, requestID)
	a.mu.Unlock()
	if !ok {
		return actor, domain.NewError(domain.CodeUnauthorized, "", nil)
	}
	if !actor.Role.Allows(required) {
		return actor, domain.NewError(domain.CodeForbidden, "", nil)
	}
	return actor, nil
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
		var at string
		if err = rows.Scan(&x.ID, &x.PostID, &x.Reason, &at); err != nil {
			return out, err
		}
		x.CreatedAt, _ = time.Parse(time.RFC3339Nano, at)
		if len(out.Items) == limit {
			out.NextCursor = out.Items[len(out.Items)-1].ID
			break
		}
		out.Items = append(out.Items, x)
	}
	return out, rows.Err()
}
func (a *Admin) SetPostVisibility(ctx context.Context, id string, visible bool, reason, requestID string) error {
	actor, err := a.actor(requestID, security.RoleModerator)
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
	actor, err := a.actor(requestID, security.RoleModerator)
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
	actor, err := a.actor(requestID, security.RoleModerator)
	var id int64
	if err != nil {
		return api.Ban{}, errors.Join(err, a.audit(ctx, actor.ID, "ban.create", "", b.Reason, requestID, err))
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
func (a *Admin) RevokeBan(ctx context.Context, id, reason, requestID string) error {
	actor, err := a.actor(requestID, security.RoleModerator)
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
	actor, err := a.actor(requestID, security.RoleAdministrator)
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
	if actionErr != nil {
		outcome = "failure"
	}
	_, err := executor.ExecContext(ctx, `INSERT INTO moderation_actions(admin_id,action,target_id,reason,outcome,request_id,payload,created_at) VALUES(NULLIF(?,''),?,?,?,?,?,?,?)`, actor, action, target, reason, outcome, requestID, []byte(`{}`), encodeTime(entry.CreatedAt))
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
