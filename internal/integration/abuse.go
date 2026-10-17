package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"bord/internal/api"
	"bord/internal/app/moderation"
	"bord/internal/domain"
	"bord/internal/security"
)

type Abuse struct {
	db         *sql.DB
	clock      Clock
	lookupKey  []byte
	keyring    security.IPKeyring
	storeRawIP bool
	limiter    *security.Limiter
	pow        *security.PoW
	mu         sync.Mutex
	pending    map[string]int64
}

func (a *Abuse) subject(ctx context.Context) (netip.Addr, []byte, string, error) {
	ip, err := netip.ParseAddr(api.ClientIP(ctx))
	if err != nil {
		return netip.Addr{}, nil, "", err
	}
	lookup, err := security.IPLookup(a.lookupKey, ip)
	if err != nil {
		return netip.Addr{}, nil, "", err
	}
	prefix, _ := security.IPPrefix(ip)
	return ip, lookup[:], prefix.String(), nil
}
func (a *Abuse) Check(ctx context.Context, operation, solution string) error {
	return a.check(ctx, operation, solution, "")
}

// CheckSession adds a pseudonymous session bucket when the frozen HTTP request
// carries a session proof. Only a domain-separated digest enters limiter state.
func (a *Abuse) CheckSession(ctx context.Context, operation, solution, sessionProof string) error {
	return a.check(ctx, operation, solution, sessionProof)
}

func (a *Abuse) check(ctx context.Context, operation, solution, sessionProof string) error {
	ip, lookup, prefix, err := a.subject(ctx)
	if err != nil {
		return domain.NewError(domain.CodeValidation, "source", err)
	}
	bans, err := a.bans(ctx)
	if err != nil {
		return err
	}
	if _, blocked := security.EvaluateBans(bans, security.BanSubject{IP: ip, Lookup: lookup, Operation: operation}, a.clock.Now()); blocked {
		return domain.NewError(domain.CodeForbidden, "", errors.New("write rejected"))
	}
	class := "write"
	if operation == "report" {
		class = "report"
	}
	keys := []security.BucketKey{
		{Dimension: "ip", Subject: hex.EncodeToString(lookup), Operation: operation, Class: class},
		{Dimension: "prefix", Subject: prefix, Operation: operation, Class: class},
	}
	if sessionProof != "" {
		digest := sha256.Sum256([]byte("bord/rate-limit-session/v1\x00" + sessionProof))
		keys = append(keys, security.BucketKey{Dimension: "session", Subject: hex.EncodeToString(digest[:]), Operation: operation, Class: class})
	}
	allowed, retry := a.limiter.Allow(keys...)
	if !allowed {
		if solution == "" {
			return &api.RetryError{Err: domain.NewError(domain.CodeChallengeRequired, "", nil), After: retry}
		}
		token, raw, ok := strings.Cut(solution, ":")
		if !ok {
			return domain.NewError(domain.CodeInvalidChallenge, "pow_solution", nil)
		}
		n, e := strconv.ParseUint(raw, 10, 64)
		if e != nil || a.pow.Verify(token, operation, hex.EncodeToString(lookup), n) != nil {
			return domain.NewError(domain.CodeInvalidChallenge, "pow_solution", nil)
		}
	}
	requestID := api.RequestID(ctx)
	a.mu.Lock()
	if requestID == "" {
		a.mu.Unlock()
		return domain.NewError(domain.CodeInternal, "", errors.New("missing request ID"))
	}
	if _, collision := a.pending[requestID]; collision {
		a.mu.Unlock()
		return domain.NewError(domain.CodeForbidden, "", errors.New("duplicate active request ID"))
	}
	// Reserve before persistence so concurrent requests carrying the same
	// trusted-edge correlation ID cannot overwrite each other's subject state.
	a.pending[requestID] = 0
	a.mu.Unlock()
	sid, err := a.storeSubject(ctx, ip, lookup, prefix)
	if err != nil {
		a.mu.Lock()
		delete(a.pending, requestID)
		a.mu.Unlock()
		return err
	}
	a.mu.Lock()
	a.pending[requestID] = sid
	a.mu.Unlock()
	return nil
}

// CheckRead enforces production read limits without extending the frozen
// AbuseService port. Reads do not persist request subjects or require PoW.
func (a *Abuse) CheckRead(ctx context.Context, operation string) error {
	_, lookup, prefix, err := a.subject(ctx)
	if err != nil {
		return domain.NewError(domain.CodeValidation, "source", err)
	}
	allowed, retry := a.limiter.Allow(
		security.BucketKey{Dimension: "ip", Subject: hex.EncodeToString(lookup), Operation: operation, Class: "read"},
		security.BucketKey{Dimension: "prefix", Subject: prefix, Operation: operation, Class: "read"},
	)
	if !allowed {
		return &api.RetryError{Err: domain.NewError(domain.CodeRateLimited, "", nil), After: retry}
	}
	return nil
}
func (a *Abuse) Challenge(ctx context.Context, purpose string) (api.Challenge, error) {
	_, lookup, _, err := a.subject(ctx)
	if err != nil {
		return api.Challenge{}, domain.NewError(domain.CodeValidation, "source", err)
	}
	token, err := a.pow.Issue(purpose, hex.EncodeToString(lookup), 8, time.Minute)
	if err != nil {
		return api.Challenge{}, err
	}
	return api.Challenge{Token: token, Difficulty: 8, ExpiresAt: a.clock.Now().Add(time.Minute)}, nil
}
func (a *Abuse) storeSubject(ctx context.Context, ip netip.Addr, lookup []byte, prefix string) (int64, error) {
	var ciphertext, nonce, keyID any
	if a.storeRawIP {
		env, err := a.keyring.Encrypt(ip, randReader{})
		if err != nil {
			return 0, err
		}
		ciphertext = env.Ciphertext
		nonce = env.Nonce
		keyID = env.KeyID
	}
	res, err := a.db.ExecContext(ctx, `INSERT INTO request_subjects(ip_lookup,ip_prefix,encrypted_ip,nonce,key_id,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`, lookup, []byte(prefix), ciphertext, nonce, keyID, encodeTime(a.clock.Now()), encodeTime(a.clock.Now().Add(90*24*time.Hour)))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type randReader struct{}

func (randReader) Read(p []byte) (int, error) { return rand.Read(p) }

func (a *Abuse) subjectID(ctx context.Context) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.pending[api.RequestID(ctx)]
	delete(a.pending, api.RequestID(ctx))
	return id
}
func (a *Abuse) discard(ctx context.Context) {
	a.mu.Lock()
	delete(a.pending, api.RequestID(ctx))
	a.mu.Unlock()
}
func (a *Abuse) bindPost(ctx context.Context, post string) {
	a.mu.Lock()
	sid := a.pending[api.RequestID(ctx)]
	delete(a.pending, api.RequestID(ctx))
	a.mu.Unlock()
	if sid != 0 {
		_, _ = a.db.ExecContext(ctx, `UPDATE request_subjects SET post_id=? WHERE id=?`, post, sid)
	}
}
func (a *Abuse) bans(ctx context.Context) ([]security.Ban, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id,subject_kind,subject_value,coalesce(operation,''),expires_at FROM bans WHERE revoked_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []security.Ban
	for rows.Next() {
		var id, kind, op, expiry string
		var value []byte
		if err := rows.Scan(&id, &kind, &value, &op, &expiry); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, expiry)
		if err != nil {
			return nil, err
		}
		b := security.Ban{ID: id, Operation: op, ExpiresAt: at}
		switch kind {
		case "ip":
			v, e := netip.ParseAddr(string(value))
			if e == nil {
				b.IP = &v
			}
		case "prefix":
			v, e := netip.ParsePrefix(string(value))
			if e == nil {
				b.Prefix = &v
			}
		case "lookup":
			b.Lookup = value
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

type challengeUses struct{ db *sql.DB }

func (s *challengeUses) Use(id string, expires, now time.Time) bool {
	tx, err := s.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	_, _ = tx.Exec(`DELETE FROM used_challenges WHERE expires_at<=?`, encodeTime(now))
	if _, err = tx.Exec(`INSERT INTO used_challenges(id,expires_at) VALUES(?,?)`, id, encodeTime(expires)); err != nil {
		return false
	}
	return tx.Commit() == nil
}

// Retain removes personal and replay-protection data at exact boundaries while
// preserving legal holds and subjects covered by an active matching ban.
func (a *Abuse) Retain(ctx context.Context) (moderation.RetentionResult, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return moderation.RetentionResult{}, err
	}
	defer tx.Rollback()
	job, err := moderation.NewRetentionJob(retentionRepo{executor: tx}, a.clock, moderation.DefaultRetention)
	if err != nil {
		return moderation.RetentionResult{}, err
	}
	result, err := job.Run(ctx)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

type retentionRepo struct{ executor auditExecutor }

func (r retentionRepo) DeleteExpiredIP(ctx context.Context, cutoff, now time.Time) (int64, error) {
	res, e := r.executor.ExecContext(ctx, `DELETE FROM request_subjects WHERE created_at<=? AND legal_hold=0 AND NOT EXISTS (SELECT 1 FROM bans WHERE revoked_at IS NULL AND expires_at>? AND ((subject_kind='lookup' AND subject_value=request_subjects.ip_lookup) OR (subject_kind='prefix' AND subject_value=request_subjects.ip_prefix)))`, encodeTime(cutoff), encodeTime(now))
	if e != nil {
		return 0, e
	}
	return res.RowsAffected()
}
func (r retentionRepo) DeleteExpiredIdempotency(ctx context.Context, cutoff time.Time) (int64, error) {
	res, e := r.executor.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE created_at<=?`, encodeTime(cutoff))
	if e != nil {
		return 0, e
	}
	return res.RowsAffected()
}
func (r retentionRepo) DeleteExpiredChallenges(ctx context.Context, now time.Time) (int64, error) {
	res, e := r.executor.ExecContext(ctx, `DELETE FROM used_challenges WHERE expires_at<=?`, encodeTime(now))
	if e != nil {
		return 0, e
	}
	return res.RowsAffected()
}
