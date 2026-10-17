package integration

import (
	"context"
	"crypto/rand"
	"database/sql"
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
	_ = a.audit(ctx, identity.ID, "admin.authenticate", "", "", requestID, err)
	if err != nil {
		return mapAdminError(err)
	}
	a.mu.Lock()
	a.actors[requestID] = identity
	a.mu.Unlock()
	return nil
}
func (a *Admin) actor(requestID string, required security.Role) (security.AdminIdentity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	actor, ok := a.actors[requestID]
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
	if err == nil {
		query := `UPDATE posts SET hidden_body=body,hidden_attachment_id=attachment_id,body='',attachment_id=NULL,status='hidden' WHERE id=? AND status='visible'`
		if visible {
			query = `UPDATE posts SET body=coalesce(hidden_body,''),attachment_id=hidden_attachment_id,hidden_body=NULL,hidden_attachment_id=NULL,status='visible' WHERE id=? AND status='hidden'`
		}
		res, e := a.db.ExecContext(ctx, query, id)
		err = e
		if e == nil {
			if n, _ := res.RowsAffected(); n != 1 {
				err = domain.NewError(domain.CodePostNotFound, "post_id", nil)
			}
		}
	}
	return errors.Join(err, a.audit(ctx, actor.ID, "post.visibility", id, reason, requestID, err))
}
func (a *Admin) CloseThread(ctx context.Context, id, reason, requestID string) error {
	actor, err := a.actor(requestID, security.RoleModerator)
	if err == nil {
		res, e := a.db.ExecContext(ctx, `UPDATE threads SET status='closed' WHERE id=?`, id)
		err = e
		if e == nil {
			if n, _ := res.RowsAffected(); n != 1 {
				err = domain.NewError(domain.CodeThreadNotFound, "thread_id", nil)
			}
		}
	}
	return errors.Join(err, a.audit(ctx, actor.ID, "thread.close", id, reason, requestID, err))
}
func (a *Admin) CreateBan(ctx context.Context, b api.BanRequest, requestID string) (api.Ban, error) {
	actor, err := a.actor(requestID, security.RoleModerator)
	var id int64
	if err == nil {
		subjectKind, subjectValue := b.SubjectType, []byte(b.Subject)
		if b.SubjectType == "ip" {
			address, parseErr := netip.ParseAddr(b.Subject)
			if parseErr != nil {
				err = domain.NewError(domain.CodeValidation, "subject", parseErr)
			} else if lookup, lookupErr := security.IPLookup(a.lookupKey, address); lookupErr != nil {
				err = lookupErr
			} else {
				subjectKind, subjectValue = "lookup", lookup[:]
			}
		}
		for _, op := range b.Operations {
			if err != nil {
				break
			}
			res, e := a.db.ExecContext(ctx, `INSERT INTO bans(subject_kind,subject_value,operation,reason,created_at,expires_at) VALUES(?,?,?,?,?,?)`, subjectKind, subjectValue, op, b.Reason, encodeTime(a.clock.Now()), encodeTime(b.ExpiresAt))
			if e != nil {
				err = e
				break
			}
			if id == 0 {
				id, _ = res.LastInsertId()
			}
		}
	}
	auditErr := a.audit(ctx, actor.ID, "ban.create", strconv.FormatInt(id, 10), b.Reason, requestID, err)
	if err != nil || auditErr != nil {
		return api.Ban{}, errors.Join(err, auditErr)
	}
	return api.Ban{ID: fmt.Sprintf("%08d", id), ExpiresAt: b.ExpiresAt}, nil
}
func (a *Admin) RevokeBan(ctx context.Context, id, reason, requestID string) error {
	actor, err := a.actor(requestID, security.RoleModerator)
	if err == nil {
		_, err = a.db.ExecContext(ctx, `UPDATE bans SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, encodeTime(a.clock.Now()), id)
	}
	return errors.Join(err, a.audit(ctx, actor.ID, "ban.revoke", id, reason, requestID, err))
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
	_, err := a.db.ExecContext(ctx, `INSERT INTO moderation_actions(admin_id,action,target_id,reason,outcome,request_id,payload,created_at) VALUES(NULLIF(?,''),?,?,?,?,?,?,?)`, actor, action, target, reason, outcome, requestID, []byte(`{}`), encodeTime(entry.CreatedAt))
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
