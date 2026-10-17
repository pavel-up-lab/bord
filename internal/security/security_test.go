package security

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

var testArgon = Argon2Params{Version: 1, Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, KeyLength: 16}

func TestSecretVectorsAndVerifierNegatives(t *testing.T) {
	secret, err := GenerateSessionSecret(bytes.NewReader(bytes.Repeat([]byte{0xab}, SecretSize)))
	if err != nil || len(secret) != SecretSize {
		t.Fatalf("GenerateSecret() = %x, %v", secret, err)
	}
	deleteSecret, err := GenerateDeleteSecret(bytes.NewReader(bytes.Repeat([]byte{0xcd}, SecretSize)))
	if err != nil || len(deleteSecret) != SecretSize {
		t.Fatalf("GenerateDeleteSecret() = %x, %v", deleteSecret, err)
	}
	fingerprint, err := SessionFingerprint(bytes.Repeat([]byte{0x11}, SecretSize))
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(fingerprint[:]); got != "7d66861c85e6eebc651db3d70fb624797b31ef1723d54b80959442117edaa431" {
		t.Fatalf("fingerprint vector = %s", got)
	}
	author, err := ThreadAuthorID([]byte("01234567890123456789012345678901"), fingerprint, "thread-42")
	if err != nil {
		t.Fatal(err)
	}
	if author != "makthtx6fe4njwhfoqfa" {
		t.Fatalf("author vector = %s", author)
	}
	otherAuthor, _ := ThreadAuthorID([]byte("01234567890123456789012345678901"), fingerprint, "thread-43")
	if author == otherAuthor {
		t.Fatal("author id linked across threads")
	}
	if _, err := SessionFingerprint([]byte("short")); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("short session = %v", err)
	}
	if _, err := ThreadAuthorID([]byte("short"), fingerprint, "thread"); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("short author key = %v", err)
	}
	encoded, err := HashSecret([]byte("correct horse battery staple"), testArgon, bytes.NewReader([]byte("12345678")))
	if err != nil {
		t.Fatal(err)
	}
	if encoded != "$bord-argon2id$v=1$m=64,t=1,p=1$MTIzNDU2Nzg$eHvmTxQel6/xFPpmcLFwEg" {
		t.Fatalf("argon vector = %s", encoded)
	}
	if !VerifySecret([]byte("correct horse battery staple"), encoded) {
		t.Fatal("correct secret rejected")
	}
	for _, invalid := range []struct{ secret, verifier string }{{"wrong", encoded}, {"x", "bad"}, {"x", strings.Replace(encoded, "v=1", "v=99", 1)}, {"x", "$bord-argon2id$v=1$m=999999,t=1,p=1$MTIzNDU2Nzg$AA"}} {
		if VerifySecret([]byte(invalid.secret), invalid.verifier) {
			t.Fatalf("accepted invalid verifier %q", invalid.verifier)
		}
	}
	if _, err := GenerateSecret(bytes.NewReader(nil)); err == nil {
		t.Fatal("short entropy accepted")
	}
}

func TestTrustedProxyCanonicalLookupAndEnvelopeVectors(t *testing.T) {
	resolver, err := NewProxyResolver([]string{"10.0.0.0/8", "2001:db8:ffff::/48"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ remote, forwarded, want string }{
		{"203.0.113.9:4444", "198.51.100.1", "203.0.113.9"},
		{"10.0.0.2:4444", "198.51.100.1, 10.1.1.1", "198.51.100.1"},
		{"10.0.0.2:4444", "garbage", "10.0.0.2"},
		{"[2001:db8:ffff::2]:4444", "2001:db8::7", "2001:db8::7"},
	}
	for _, tt := range tests {
		got, err := resolver.Resolve(tt.remote, tt.forwarded)
		if err != nil || got.String() != tt.want {
			t.Errorf("Resolve(%q) = %s, %v; want %s", tt.remote, got, err, tt.want)
		}
	}
	v4 := netip.MustParseAddr("192.0.2.129")
	canonical, _ := CanonicalIP(v4)
	if got := hex.EncodeToString(canonical[:]); got != "00000000000000000000ffffc0000281" {
		t.Fatalf("canonical vector = %s", got)
	}
	prefix, _ := IPPrefix(v4)
	if prefix.String() != "192.0.2.0/24" {
		t.Fatalf("v4 prefix = %s", prefix)
	}
	v6prefix, _ := IPPrefix(netip.MustParseAddr("2001:db8:abcd:1234::9"))
	if v6prefix.String() != "2001:db8:abcd:1234::/64" {
		t.Fatalf("v6 prefix = %s", v6prefix)
	}
	lookup, _ := IPLookup(bytes.Repeat([]byte{0x22}, SecretSize), v4)
	if got := hex.EncodeToString(lookup[:]); got != "b0717f0cfb50965ee7665f6ed2c479c642de88279c2123c4db639ba7ee64eb16" {
		t.Fatalf("lookup vector = %s", got)
	}
	if _, err := IPLookup([]byte("short"), v4); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("short lookup key = %v", err)
	}
	oldKey, newKey := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	oldRing := IPKeyring{Primary: "old", Keys: map[string][]byte{"old": oldKey}}
	envelope, err := oldRing.Encrypt(v4, bytes.NewReader(bytes.Repeat([]byte{3}, 12)))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Nonce != "AwMDAwMDAwMDAwMD" || envelope.Ciphertext != "+GEUmv+EN2mt/BmYDBkmKc8QfRj84USbPS1B1fTXMvQ" {
		t.Fatalf("envelope vector = %+v", envelope)
	}
	rotated := IPKeyring{Primary: "new", Keys: map[string][]byte{"old": oldKey, "new": newKey}}
	plain, err := rotated.Decrypt(envelope)
	if err != nil || plain != v4 {
		t.Fatalf("rotation read = %s, %v", plain, err)
	}
	tampered := envelope
	tampered.Ciphertext = tampered.Ciphertext[:len(tampered.Ciphertext)-1] + "A"
	if _, err := rotated.Decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	delete(rotated.Keys, "old")
	if _, err := rotated.Decrypt(envelope); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key = %v", err)
	}
}

func TestLimiterBurstRefillAndOperationIsolation(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	limiter, err := NewLimiter(clock, map[string]Rate{"read": {Capacity: 2, RefillEvery: time.Second}, "write": {Capacity: 1, RefillEvery: 2 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	read := BucketKey{Dimension: "ip", Subject: "lookup", Operation: "thread", Class: "read"}
	write := BucketKey{Dimension: "ip", Subject: "lookup", Operation: "reply", Class: "write"}
	sessionWrite := BucketKey{Dimension: "session", Subject: "fingerprint", Operation: "reply", Class: "write"}
	if ok, _ := limiter.Allow(read); !ok {
		t.Fatal("read 1 rejected")
	}
	if ok, _ := limiter.Allow(read); !ok {
		t.Fatal("read 2 rejected")
	}
	if ok, retry := limiter.Allow(read); ok || retry != time.Second {
		t.Fatalf("read burst = %v, %s", ok, retry)
	}
	if ok, _ := limiter.Allow(write); !ok {
		t.Fatal("independent write rejected")
	}
	if ok, retry := limiter.Allow(BucketKey{Dimension: "prefix", Subject: "192.0.2.0/24", Operation: "reply", Class: "write"}, sessionWrite); !ok || retry != 0 {
		t.Fatalf("multi-dimensional write = %v, %s", ok, retry)
	}
	if ok, retry := limiter.Allow(write); ok || retry != 2*time.Second {
		t.Fatalf("write retry = %v, %s", ok, retry)
	}
	clock.now = clock.now.Add(time.Second)
	if ok, _ := limiter.Allow(read); !ok {
		t.Fatal("refilled read rejected")
	}
	if ok, retry := limiter.Allow(write); ok || retry != time.Second {
		t.Fatalf("partial refill = %v, %s", ok, retry)
	}
}

func TestPoWPurposeExpiryDifficultyAndReplay(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000, 0)}
	pow, err := NewPoW(bytes.Repeat([]byte{7}, 32), clock, bytes.NewReader(bytes.Repeat([]byte{8}, 48)), NewMemoryChallengeUses(), 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pow.Issue("reply", "subject", 17, time.Minute); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("difficulty bound = %v", err)
	}
	token, err := pow.Issue("reply", "subject", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	solution, err := Solve(context.Background(), token, 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := pow.Verify(token, "thread", "subject", solution); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("purpose mismatch = %v", err)
	}
	if err := pow.Verify(token, "reply", "subject", solution); err != nil {
		t.Fatal(err)
	}
	if err := pow.Verify(token, "reply", "subject", solution); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("replay = %v", err)
	}
	expiring, _ := pow.Issue("reply", "subject", 8, time.Second)
	expiringSolution, _ := Solve(context.Background(), expiring, 8, 1<<18)
	clock.now = clock.now.Add(time.Second)
	if err := pow.Verify(expiring, "reply", "subject", expiringSolution); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("expiry = %v", err)
	}
}

func TestBanPriorityAndExpiry(t *testing.T) {
	now := time.Unix(2_000, 0)
	ip := netip.MustParseAddr("192.0.2.8")
	prefix := netip.MustParsePrefix("192.0.2.0/24")
	bans := []Ban{
		{ID: "operation", Operation: "reply", ExpiresAt: now.Add(time.Hour)},
		{ID: "expired", IP: &ip, ExpiresAt: now},
		{ID: "prefix", Prefix: &prefix, ExpiresAt: now.Add(time.Hour)},
		{ID: "lookup", Lookup: []byte("subject-lookup"), ExpiresAt: now.Add(time.Hour)},
		{ID: "exact", IP: &ip, Board: "b", Operation: "reply", ExpiresAt: now.Add(time.Hour)},
	}
	got, ok := EvaluateBans(bans, BanSubject{IP: ip, Board: "b", Operation: "reply"}, now)
	if !ok || got.ID != "exact" {
		t.Fatalf("ban = %+v, %v", got, ok)
	}
	got, ok = EvaluateBans(bans, BanSubject{IP: netip.MustParseAddr("198.51.100.1"), Lookup: []byte("subject-lookup"), Operation: "read"}, now)
	if !ok || got.ID != "lookup" {
		t.Fatalf("lookup ban = %+v, %v", got, ok)
	}
	if _, ok := EvaluateBans(bans, BanSubject{IP: netip.MustParseAddr("198.51.100.1"), Operation: "read"}, now); ok {
		t.Fatal("unmatched subject banned")
	}
}

func TestAdminAuthenticationRolesExpiryAndRevocation(t *testing.T) {
	clock := &fakeClock{now: time.Unix(3_000, 0)}
	repo := NewMemoryAdminIdentities()
	dummy, _ := HashSecret([]byte("dummy-secret-value"), testArgon, bytes.NewReader([]byte("abcdefgh")))
	credential, identity, err := NewAdminCredential("alice", RoleModerator, clock.now.Add(time.Hour), bytes.NewReader(bytes.Repeat([]byte{9}, 128)), testArgon)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	auth := NewAdminAuthenticator(repo, clock, dummy)
	if _, err := auth.Authenticate(context.Background(), credential, RoleModerator); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(context.Background(), credential, RoleAdministrator); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("role denied = %v", err)
	}
	if _, err := auth.Authenticate(context.Background(), "unknown.secret", RoleReports); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown = %v", err)
	}
	clock.now = identity.ExpiresAt
	if _, err := auth.Authenticate(context.Background(), credential, RoleReports); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired = %v", err)
	}
	clock.now = time.Unix(3_000, 0)
	revoked := clock.now
	identity.RevokedAt = &revoked
	_ = repo.Save(context.Background(), identity)
	if _, err := auth.Authenticate(context.Background(), credential, RoleReports); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked = %v", err)
	}
}
