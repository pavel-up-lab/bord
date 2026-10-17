// Package operator implements offline, fail-closed administrative workflows.
package operator

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bord/internal/security"
)

type RetentionReport struct {
	EligibleIP, LegalHoldIP, ActiveBanIP int64
	Idempotency, Challenges              int64
}

type ReencryptReport struct {
	Reencrypted int64
	Remaining   int64
}

func ParseRole(value string) (security.Role, error) {
	switch value {
	case "reports":
		return security.RoleReports, nil
	case "moderator":
		return security.RoleModerator, nil
	case "administrator":
		return security.RoleAdministrator, nil
	default:
		return 0, errors.New("role must be reports, moderator, or administrator")
	}
}

func roleName(role security.Role) string {
	switch role {
	case security.RoleReports:
		return "reports"
	case security.RoleModerator:
		return "moderator"
	case security.RoleAdministrator:
		return "administrator"
	default:
		return "invalid"
	}
}

// CreateCredential writes the sole credential copy to an exclusive 0600 file.
// The database change and audit entry commit together; no secret is returned.
func CreateCredential(ctx context.Context, db *sql.DB, id string, role security.Role, ttl time.Duration, output string, rotate bool, now time.Time) error {
	if ttl <= 0 || output == "" || !role.Valid() {
		return errors.New("invalid credential parameters")
	}
	credential, identity, err := security.NewAdminCredential(id, role, now.Add(ttl), rand.Reader, security.DefaultArgon2Params)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if rotate {
		res, execErr := tx.ExecContext(ctx, `UPDATE admin_identities SET role=?,credential_verifier=?,expires_at=?,revoked_at=NULL WHERE id=?`, roleName(role), []byte(identity.Verifier), identity.ExpiresAt.UTC().Format(time.RFC3339Nano), id)
		if execErr != nil {
			return execErr
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errors.New("administrator identity not found")
		}
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO admin_identities(id,role,credential_verifier,created_at,expires_at) VALUES(?,?,?,?,?)`, id, roleName(role), []byte(identity.Verifier), now.UTC().Format(time.RFC3339Nano), identity.ExpiresAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("create administrator identity: %w", err)
		}
	}
	action := "admin.create"
	if rotate {
		action = "admin.rotate"
	}
	if err := audit(ctx, tx, id, action, id, "offline operator command", now); err != nil {
		return err
	}
	if err := writeCredential(output, []byte(credential+"\n")); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		_ = os.Remove(output)
		return err
	}
	return nil
}

func writeCredential(path string, value []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bord-credential-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(value)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("credential output already exists")
	}
	return os.Rename(name, path)
}

func RevokeCredential(ctx context.Context, db *sql.DB, id, reason string, now time.Time) error {
	if strings.TrimSpace(reason) == "" || security.ContainsRawIP(reason) {
		return errors.New("a non-sensitive reason is required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE admin_identities SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, now.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("active administrator identity not found")
	}
	if err := audit(ctx, tx, id, "admin.revoke", id, reason, now); err != nil {
		return err
	}
	return tx.Commit()
}

func audit(ctx context.Context, tx *sql.Tx, actor, action, target, reason string, now time.Time) error {
	requestID := fmt.Sprintf("offline-%d", now.UnixNano())
	_, err := tx.ExecContext(ctx, `INSERT INTO moderation_actions(admin_id,action,target_id,reason,outcome,request_id,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, actor, action, target, reason, "success", requestID, []byte(`{}`), now.UTC().Format(time.RFC3339Nano))
	return err
}

func InspectRetention(ctx context.Context, db *sql.DB, now time.Time) (RetentionReport, error) {
	cutoff := now.Add(-90 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	nowText := now.UTC().Format(time.RFC3339Nano)
	var out RetentionReport
	queries := []struct {
		to *int64
		q  string
		a  []any
	}{
		{&out.EligibleIP, `SELECT count(*) FROM request_subjects r WHERE r.created_at<=? AND r.legal_hold=0 AND NOT EXISTS (SELECT 1 FROM bans b WHERE b.revoked_at IS NULL AND b.expires_at>? AND ((b.subject_kind='lookup' AND b.subject_value=r.ip_lookup) OR (b.subject_kind='prefix' AND b.subject_value=r.ip_prefix)))`, []any{cutoff, nowText}},
		{&out.LegalHoldIP, `SELECT count(*) FROM request_subjects WHERE created_at<=? AND legal_hold=1`, []any{cutoff}},
		{&out.ActiveBanIP, `SELECT count(*) FROM request_subjects r WHERE r.created_at<=? AND r.legal_hold=0 AND EXISTS (SELECT 1 FROM bans b WHERE b.revoked_at IS NULL AND b.expires_at>? AND ((b.subject_kind='lookup' AND b.subject_value=r.ip_lookup) OR (b.subject_kind='prefix' AND b.subject_value=r.ip_prefix)))`, []any{cutoff, nowText}},
		{&out.Idempotency, `SELECT count(*) FROM idempotency_keys WHERE created_at<=?`, []any{cutoff}},
		{&out.Challenges, `SELECT count(*) FROM used_challenges WHERE expires_at<=?`, []any{nowText}},
	}
	for _, item := range queries {
		if err := db.QueryRowContext(ctx, item.q, item.a...).Scan(item.to); err != nil {
			return out, err
		}
	}
	return out, nil
}

func RunRetention(ctx context.Context, db *sql.DB, now time.Time) (RetentionReport, error) {
	before, err := InspectRetention(ctx, db, now)
	if err != nil {
		return before, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return before, err
	}
	defer tx.Rollback()
	cutoff := now.Add(-90 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	nowText := now.UTC().Format(time.RFC3339Nano)
	statements := []struct {
		q string
		a []any
	}{
		{`DELETE FROM request_subjects WHERE created_at<=? AND legal_hold=0 AND NOT EXISTS (SELECT 1 FROM bans WHERE revoked_at IS NULL AND expires_at>? AND ((subject_kind='lookup' AND subject_value=request_subjects.ip_lookup) OR (subject_kind='prefix' AND subject_value=request_subjects.ip_prefix)))`, []any{cutoff, nowText}},
		{`DELETE FROM idempotency_keys WHERE created_at<=?`, []any{cutoff}},
		{`DELETE FROM used_challenges WHERE expires_at<=?`, []any{nowText}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.q, statement.a...); err != nil {
			return before, err
		}
	}
	return before, tx.Commit()
}

func SetLegalHold(ctx context.Context, db *sql.DB, subjectID int64, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	res, err := db.ExecContext(ctx, `UPDATE request_subjects SET legal_hold=? WHERE id=?`, value, subjectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("request subject not found")
	}
	return nil
}

func ReadKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("key file must contain one base64-encoded 32-byte key")
	}
	return key, nil
}

// Reencrypt processes bounded transactions. A rerun skips rows already tagged
// with toID, so an interruption is safe and resumable.
func Reencrypt(ctx context.Context, db *sql.DB, fromID string, fromKey []byte, toID string, toKey []byte, batch int) (ReencryptReport, error) {
	if fromID == "" || toID == "" || fromID == toID || len(fromKey) != 32 || len(toKey) != 32 || batch < 1 || batch > 1000 {
		return ReencryptReport{}, errors.New("invalid re-encryption parameters")
	}
	keys := security.IPKeyring{Primary: toID, Keys: map[string][]byte{fromID: fromKey, toID: toKey}}
	var report ReencryptReport
	for {
		rows, err := db.QueryContext(ctx, `SELECT id,nonce,encrypted_ip FROM request_subjects WHERE encrypted_ip IS NOT NULL AND key_id=? ORDER BY id LIMIT ?`, fromID, batch)
		if err != nil {
			return report, err
		}
		type row struct {
			id                int64
			nonce, ciphertext string
		}
		var items []row
		for rows.Next() {
			var x row
			if err := rows.Scan(&x.id, &x.nonce, &x.ciphertext); err != nil {
				rows.Close()
				return report, err
			}
			items = append(items, x)
		}
		err = rows.Close()
		if err != nil {
			return report, err
		}
		if len(items) == 0 {
			break
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return report, err
		}
		for _, item := range items {
			ip, decryptErr := keys.Decrypt(security.IPEnvelope{Version: 1, KeyID: fromID, Nonce: item.nonce, Ciphertext: item.ciphertext})
			if decryptErr != nil {
				tx.Rollback()
				return report, fmt.Errorf("decrypt request subject %d: %w", item.id, decryptErr)
			}
			envelope, encryptErr := keys.Encrypt(ip, rand.Reader)
			if encryptErr != nil {
				tx.Rollback()
				return report, encryptErr
			}
			res, updateErr := tx.ExecContext(ctx, `UPDATE request_subjects SET key_id=?,nonce=?,encrypted_ip=? WHERE id=? AND key_id=?`, envelope.KeyID, envelope.Nonce, envelope.Ciphertext, item.id, fromID)
			if updateErr != nil {
				tx.Rollback()
				return report, updateErr
			}
			if n, _ := res.RowsAffected(); n != 1 {
				tx.Rollback()
				return report, errors.New("concurrent re-encryption detected")
			}
		}
		if err := tx.Commit(); err != nil {
			return report, err
		}
		report.Reencrypted += int64(len(items))
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM request_subjects WHERE encrypted_ip IS NOT NULL AND key_id<>?`, toID).Scan(&report.Remaining); err != nil {
		return report, err
	}
	return report, nil
}
