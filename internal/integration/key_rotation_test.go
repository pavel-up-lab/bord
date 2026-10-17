package integration

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	storage "bord/internal/storage/sqlite"
)

func TestReadinessFailsClosedForUnknownIPEncryptionKey(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir() + "/bord.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(Config{DB: db, Blobs: t.TempDir(), LookupKey: bytes.Repeat([]byte{1}, 32), EncryptionKey: bytes.Repeat([]byte{2}, 32), EncryptionKeyID: "current", StoreRawIP: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO request_subjects(ip_lookup,encrypted_ip,nonce,key_id,created_at,expires_at) VALUES(?,?,?,?,?,?)`, make([]byte, 32), "ciphertext", "nonce", "missing", now.Format(time.RFC3339Nano), now.Add(time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ready(ctx); err == nil || !strings.Contains(err.Error(), "unavailable key id") {
		t.Fatalf("ready error=%v", err)
	}
}
