package operator

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bord/internal/security"
	storesqlite "bord/internal/storage/sqlite"
)

func fixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	db, err := storesqlite.Open(filepath.Join(root, "bord.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storesqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, root
}

func TestCredentialLifecycleNeverReturnsSecret(t *testing.T) {
	db, root := fixture(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	first := filepath.Join(root, "first.credential")
	if err := CreateCredential(context.Background(), db, "alice", security.RoleAdministrator, time.Hour, first, false, now); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 40 {
		t.Fatal("credential file is unexpectedly short")
	}
	if info, _ := os.Stat(first); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	second := filepath.Join(root, "second.credential")
	if err := CreateCredential(context.Background(), db, "alice", security.RoleModerator, 2*time.Hour, second, true, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := RevokeCredential(context.Background(), db, "alice", "rotation complete", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var revoked string
	if err := db.QueryRow(`SELECT revoked_at FROM admin_identities WHERE id='alice'`).Scan(&revoked); err != nil || revoked == "" {
		t.Fatalf("revoked=%q err=%v", revoked, err)
	}
}

func TestRetentionHonorsHoldAndActiveBan(t *testing.T) {
	db, _ := fixture(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	old := now.Add(-91 * 24 * time.Hour).Format(time.RFC3339Nano)
	expires := now.Add(time.Hour).Format(time.RFC3339Nano)
	for i, hold := range []int{0, 1, 0} {
		lookup := []byte{byte(i + 1)}
		if _, err := db.Exec(`INSERT INTO request_subjects(ip_lookup,created_at,expires_at,legal_hold) VALUES(?,?,?,?)`, lookup, old, expires, hold); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO bans(subject_kind,subject_value,reason,created_at,expires_at) VALUES('lookup',?,'abuse',?,?)`, []byte{3}, now.Format(time.RFC3339Nano), expires); err != nil {
		t.Fatal(err)
	}
	report, err := RunRetention(context.Background(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.EligibleIP != 1 || report.LegalHoldIP != 1 || report.ActiveBanIP != 1 {
		t.Fatalf("report=%+v", report)
	}
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM request_subjects`).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
}

func TestReencryptRoundTripAndResume(t *testing.T) {
	db, _ := fixture(t)
	oldKey := make([]byte, 32)
	newKey := make([]byte, 32)
	for i := range oldKey {
		oldKey[i] = 1
		newKey[i] = 2
	}
	oldRing := security.IPKeyring{Primary: "old", Keys: map[string][]byte{"old": oldKey}}
	env, err := oldRing.Encrypt(netip.MustParseAddr("192.0.2.44"), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO request_subjects(ip_lookup,encrypted_ip,nonce,key_id,created_at,expires_at) VALUES(?,?,?,?,?,?)`, make([]byte, 32), env.Ciphertext, env.Nonce, env.KeyID, time.Now().UTC().Format(time.RFC3339Nano), time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	report, err := Reencrypt(context.Background(), db, "old", oldKey, "new", newKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	if report.Reencrypted != 1 || report.Remaining != 0 {
		t.Fatalf("report=%+v", report)
	}
	report, err = Reencrypt(context.Background(), db, "old", oldKey, "new", newKey, 1)
	if err != nil || report.Reencrypted != 0 {
		t.Fatalf("resume=%+v err=%v", report, err)
	}
	var nonce, ciphertext, id string
	if err := db.QueryRow(`SELECT nonce,encrypted_ip,key_id FROM request_subjects`).Scan(&nonce, &ciphertext, &id); err != nil {
		t.Fatal(err)
	}
	ip, err := (security.IPKeyring{Primary: "new", Keys: map[string][]byte{"new": newKey}}).Decrypt(security.IPEnvelope{Version: 1, KeyID: id, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil || ip.String() != "192.0.2.44" {
		t.Fatalf("ip=%s err=%v", ip, err)
	}
}
