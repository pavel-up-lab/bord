package moderation

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

var ErrReasonRequired = errors.New("moderation reason required")
var ErrRoleDenied = errors.New("role denied")

type Role uint8

const (
	RoleReports Role = iota + 1
	RoleModerator
	RoleAdministrator
)

func (r Role) Allows(required Role) bool { return r >= required }

type ContentRepository interface {
	SetPostHidden(context.Context, string, bool, time.Time) error
	SetThreadClosed(context.Context, string, time.Time) error
}

type BanRecord struct {
	ID, Subject, Board, Operation, Reason string
	ExpiresAt                             time.Time
	RevokedAt                             *time.Time
}

type BanRepository interface {
	Create(context.Context, BanRecord) error
	Revoke(context.Context, string, time.Time) error
	List(context.Context) ([]BanRecord, error)
}

type IPSubjectRepository interface {
	EnvelopeByID(context.Context, string) (EncryptedIP, error)
}

type EncryptedIP struct {
	Version                  uint8
	KeyID, Nonce, Ciphertext string
}
type IPDecrypter interface {
	DecryptIP(EncryptedIP) (netip.Addr, error)
}

type Actor struct {
	ID   string
	Role Role
}

type Service struct {
	content   ContentRepository
	bans      BanRepository
	subjects  IPSubjectRepository
	audit     AuditRepository
	decrypter IPDecrypter
	clock     interface{ Now() time.Time }
}

func NewService(content ContentRepository, bans BanRepository, subjects IPSubjectRepository, audit AuditRepository, decrypter IPDecrypter, clock interface{ Now() time.Time }) *Service {
	return &Service{content: content, bans: bans, subjects: subjects, audit: audit, decrypter: decrypter, clock: clock}
}

func (s *Service) Hide(ctx context.Context, actor Actor, postID, reason, requestID string) error {
	return s.contentAction(ctx, actor, "post.hide", postID, reason, requestID, func(now time.Time) error { return s.content.SetPostHidden(ctx, postID, true, now) })
}
func (s *Service) Restore(ctx context.Context, actor Actor, postID, reason, requestID string) error {
	return s.contentAction(ctx, actor, "post.restore", postID, reason, requestID, func(now time.Time) error { return s.content.SetPostHidden(ctx, postID, false, now) })
}
func (s *Service) Close(ctx context.Context, actor Actor, threadID, reason, requestID string) error {
	return s.contentAction(ctx, actor, "thread.close", threadID, reason, requestID, func(now time.Time) error { return s.content.SetThreadClosed(ctx, threadID, now) })
}

func (s *Service) contentAction(ctx context.Context, actor Actor, action, target, reason, requestID string, apply func(time.Time) error) error {
	now := s.clock.Now()
	err := ErrReasonRequired
	if reason != "" && actor.Role.Allows(RoleModerator) {
		err = apply(now)
	} else if reason != "" {
		err = ErrRoleDenied
	}
	return errors.Join(err, s.record(ctx, actor, action, target, reason, requestID, now, err))
}

func (s *Service) CreateBan(ctx context.Context, actor Actor, ban BanRecord, requestID string) error {
	now := s.clock.Now()
	var err error
	if ban.Reason == "" {
		err = ErrReasonRequired
	} else if !actor.Role.Allows(RoleModerator) {
		err = ErrRoleDenied
	} else if !ban.ExpiresAt.After(now) || ban.ID == "" || ban.Subject == "" {
		err = errors.New("invalid ban")
	} else {
		err = s.bans.Create(ctx, ban)
	}
	return errors.Join(err, s.record(ctx, actor, "ban.create", ban.ID, ban.Reason, requestID, now, err))
}

func (s *Service) RevokeBan(ctx context.Context, actor Actor, id, reason, requestID string) error {
	now := s.clock.Now()
	var err error
	if reason == "" {
		err = ErrReasonRequired
	} else if !actor.Role.Allows(RoleModerator) {
		err = ErrRoleDenied
	} else {
		err = s.bans.Revoke(ctx, id, now)
	}
	return errors.Join(err, s.record(ctx, actor, "ban.revoke", id, reason, requestID, now, err))
}

func (s *Service) RevealIP(ctx context.Context, actor Actor, subjectID, reason, requestID string) (netip.Addr, error) {
	now := s.clock.Now()
	if reason == "" {
		return netip.Addr{}, errors.Join(ErrReasonRequired, s.record(ctx, actor, "ip.reveal", subjectID, reason, requestID, now, ErrReasonRequired))
	}
	if !actor.Role.Allows(RoleAdministrator) {
		return netip.Addr{}, errors.Join(ErrRoleDenied, s.record(ctx, actor, "ip.reveal", subjectID, reason, requestID, now, ErrRoleDenied))
	}
	envelope, err := s.subjects.EnvelopeByID(ctx, subjectID)
	var addr netip.Addr
	if err == nil {
		addr, err = s.decrypter.DecryptIP(envelope)
	}
	auditErr := s.record(ctx, actor, "ip.reveal", subjectID, reason, requestID, now, err)
	if err != nil || auditErr != nil {
		return netip.Addr{}, errors.Join(err, auditErr)
	}
	return addr, nil
}

func (s *Service) record(ctx context.Context, actor Actor, action, target, reason, requestID string, now time.Time, actionErr error) error {
	entry := AuditEntry{ActorID: actor.ID, Action: action, TargetID: target, Reason: reason, Success: actionErr == nil, RequestID: requestID, CreatedAt: now}
	if actionErr != nil {
		entry.ErrorCode = fmt.Sprintf("%T", actionErr)
	}
	return s.audit.Append(ctx, entry)
}
