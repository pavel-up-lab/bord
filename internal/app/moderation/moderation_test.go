package moderation

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type allowReports struct {
	calls   int
	allowed bool
}

type fakeDecrypter struct {
	address netip.Addr
	err     error
}

type failingAudit struct{ err error }

func (a failingAudit) Append(context.Context, AuditEntry) error   { return a.err }
func (a failingAudit) List(context.Context) ([]AuditEntry, error) { return nil, a.err }

func (d fakeDecrypter) DecryptIP(EncryptedIP) (netip.Addr, error) { return d.address, d.err }

func (l *allowReports) AllowReport(string) (bool, time.Duration) {
	l.calls++
	return l.allowed, time.Minute
}

func TestReportsValidationDeduplicationAndLimits(t *testing.T) {
	clock := fixedClock{time.Unix(100, 0)}
	repo := NewMemoryReportRepository()
	limiter := &allowReports{allowed: true}
	service := NewReportService(repo, limiter, clock)
	created, priority, err := service.Submit(context.Background(), "r1", "p1", ReportSpam, "source-a")
	if err != nil || !created || priority != 1 {
		t.Fatalf("first report = %v, %d, %v", created, priority, err)
	}
	created, priority, err = service.Submit(context.Background(), "r2", "p1", ReportThreat, "source-a")
	if err != nil || created || priority != 1 {
		t.Fatalf("duplicate report = %v, %d, %v", created, priority, err)
	}
	created, priority, err = service.Submit(context.Background(), "r3", "p1", ReportThreat, "source-b")
	if err != nil || !created || priority != 2 {
		t.Fatalf("second source = %v, %d, %v", created, priority, err)
	}
	if _, _, err := service.Submit(context.Background(), "r4", "p1", "made-up", "source-c"); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("invalid reason = %v", err)
	}
	limiter.allowed = false
	if _, _, err := service.Submit(context.Background(), "r5", "p1", ReportOther, "source-c"); !errors.Is(err, ErrReportLimited) {
		t.Fatalf("limited = %v", err)
	}
}

func TestModerationEffectsRolesRevealAndAudit(t *testing.T) {
	now := time.Unix(10_000, 0)
	clock := fixedClock{now}
	content := NewMemoryContentRepository()
	bans := NewMemoryBanRepository()
	audit := &MemoryAuditRepository{}
	ip := netip.MustParseAddr("2001:db8::5")
	envelope := EncryptedIP{Version: 1, KeyID: "k1", Nonce: "opaque-nonce", Ciphertext: "opaque-ciphertext"}
	subjects := &MemoryIPSubjects{Items: map[string]EncryptedIP{"s1": envelope}}
	service := NewService(content, bans, subjects, audit, fakeDecrypter{address: ip}, clock)
	moderator := Actor{ID: "mod", Role: RoleModerator}
	admin := Actor{ID: "admin", Role: RoleAdministrator}
	if err := service.Hide(context.Background(), moderator, "p1", "policy", "req-1"); err != nil || !content.Hidden["p1"] {
		t.Fatalf("hide = %v", err)
	}
	if err := service.Restore(context.Background(), moderator, "p1", "appeal", "req-2"); err != nil || content.Hidden["p1"] {
		t.Fatalf("restore = %v", err)
	}
	if err := service.Close(context.Background(), moderator, "t1", "flood", "req-3"); err != nil || !content.Closed["t1"] {
		t.Fatalf("close = %v", err)
	}
	ban := BanRecord{ID: "b1", Subject: "lookup:abc", Operation: "reply", Reason: "spam", ExpiresAt: now.Add(time.Hour)}
	if err := service.CreateBan(context.Background(), moderator, ban, "req-4"); err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeBan(context.Background(), moderator, "b1", "mistake", "req-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RevealIP(context.Background(), moderator, "s1", "investigation", "req-6"); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("moderator reveal = %v", err)
	}
	got, err := service.RevealIP(context.Background(), admin, "s1", "investigation", "req-7")
	if err != nil || got != ip {
		t.Fatalf("admin reveal = %s, %v", got, err)
	}
	if err := service.Hide(context.Background(), moderator, "p2", "", "req-8"); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("missing reason = %v", err)
	}
	entries, _ := audit.List(context.Background())
	if len(entries) != 8 {
		t.Fatalf("audit count = %d", len(entries))
	}
	if entries[5].Success || !entries[6].Success || entries[7].Success {
		t.Fatalf("audit outcomes = %+v", entries)
	}
	for _, entry := range entries {
		serialized := entry.Action + entry.TargetID + entry.Reason + entry.ErrorCode
		if strings.Contains(serialized, ip.String()) || strings.Contains(serialized, envelope.Ciphertext) {
			t.Fatalf("audit leaked sensitive value: %+v", entry)
		}
	}
}

func TestAppendOnlyAuditRejectsSecretFieldsAndCopies(t *testing.T) {
	repo := &MemoryAuditRepository{}
	now := time.Unix(1, 0)
	unsafe := AuditEntry{Action: "login", RequestID: "r", CreatedAt: now, Metadata: map[string]string{"bearer_token": "value"}}
	if err := repo.Append(context.Background(), unsafe); !errors.Is(err, ErrUnsafeAudit) {
		t.Fatalf("unsafe append = %v", err)
	}
	for _, unsafe := range []AuditEntry{
		{Action: "ip.reveal", TargetID: "192.0.2.1", RequestID: "r", CreatedAt: now},
		{Action: "login", RequestID: "r", CreatedAt: now, Metadata: map[string]string{"peer": "[2001:db8::1]:443"}},
		{Action: "ban.create", RequestID: "r", CreatedAt: now, Metadata: map[string]string{"subject": "192.0.2.0/24"}},
	} {
		if err := repo.Append(context.Background(), unsafe); !errors.Is(err, ErrUnsafeAudit) {
			t.Fatalf("raw IP append = %v", err)
		}
	}
	entry := AuditEntry{Action: "login", RequestID: "r", CreatedAt: now, Metadata: map[string]string{"role": "moderator"}}
	if err := repo.Append(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	entry.Metadata["role"] = "changed"
	first, _ := repo.List(context.Background())
	first[0].Metadata["role"] = "also changed"
	second, _ := repo.List(context.Background())
	if second[0].Metadata["role"] != "moderator" {
		t.Fatal("stored audit entry was mutable")
	}
	// Compile-time API shape: the repository has append/list only, no update/delete methods.
	var _ AuditRepository = repo
}

func TestModerationRejectsWhitespaceReasonWithoutMutation(t *testing.T) {
	now := time.Unix(10_000, 0)
	content := NewMemoryContentRepository()
	audit := &MemoryAuditRepository{}
	service := NewService(content, NewMemoryBanRepository(), nil, audit, nil, fixedClock{now})
	err := service.Hide(context.Background(), Actor{ID: "mod", Role: RoleModerator}, "p1", " \t", "req")
	if !errors.Is(err, ErrReasonRequired) || content.Hidden["p1"] {
		t.Fatalf("hide = %v, hidden = %v", err, content.Hidden["p1"])
	}
	entries, listErr := audit.List(context.Background())
	if listErr != nil || len(entries) != 1 || entries[0].Success {
		t.Fatalf("audit = %+v, %v", entries, listErr)
	}
}

func TestModerationRejectsInvalidRoleWithoutMutation(t *testing.T) {
	content := NewMemoryContentRepository()
	audit := &MemoryAuditRepository{}
	service := NewService(content, NewMemoryBanRepository(), nil, audit, nil, fixedClock{time.Unix(10_000, 0)})
	err := service.Hide(context.Background(), Actor{ID: "invalid", Role: Role(255)}, "p1", "policy", "req")
	if !errors.Is(err, ErrRoleDenied) || content.Hidden["p1"] {
		t.Fatalf("hide = %v, hidden = %v", err, content.Hidden["p1"])
	}
}

func TestRevealFailsClosedWhenAuditCannotAppend(t *testing.T) {
	auditErr := errors.New("audit unavailable")
	service := NewService(nil, nil, &MemoryIPSubjects{Items: map[string]EncryptedIP{"s1": {Version: 1}}}, failingAudit{auditErr}, fakeDecrypter{address: netip.MustParseAddr("192.0.2.1")}, fixedClock{time.Unix(1, 0)})
	address, err := service.RevealIP(context.Background(), Actor{ID: "admin", Role: RoleAdministrator}, "s1", "investigation", "request")
	if !errors.Is(err, auditErr) || address.IsValid() {
		t.Fatalf("reveal = %s, %v", address, err)
	}
}

func TestRetentionDefaultBoundaryAndExceptions(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-DefaultRetention)
	repo := &MemoryRetentionRepository{
		IP:          []RetentionIP{{ID: "boundary", CreatedAt: cutoff}, {ID: "new", CreatedAt: cutoff.Add(time.Second)}, {ID: "hold", CreatedAt: cutoff.Add(-time.Hour), LegalHold: true}, {ID: "ban", CreatedAt: cutoff.Add(-time.Hour), ActiveBanUntil: now.Add(time.Hour)}, {ID: "expired-ban", CreatedAt: cutoff.Add(-time.Hour), ActiveBanUntil: now}},
		Idempotency: []RetentionValue{{ID: "old", ExpiresAt: cutoff}, {ID: "new", ExpiresAt: cutoff.Add(time.Second)}},
		Challenges:  []RetentionValue{{ID: "used-old", ExpiresAt: now}, {ID: "used-new", ExpiresAt: now.Add(time.Second)}},
	}
	job, err := NewRetentionJob(repo, fixedClock{now}, 0)
	if err != nil {
		t.Fatal(err)
	}
	result, err := job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result != (RetentionResult{IP: 2, Idempotency: 1, Challenges: 1}) {
		t.Fatalf("result = %+v", result)
	}
	if len(repo.IP) != 3 || len(repo.Idempotency) != 1 || len(repo.Challenges) != 1 {
		t.Fatalf("remaining = %+v %+v %+v", repo.IP, repo.Idempotency, repo.Challenges)
	}
}
