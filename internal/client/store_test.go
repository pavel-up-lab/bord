package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStoreRoundTripPermissionsAndCorruptRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	store := NewStore(path)
	state, err := NewState("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	state.DeleteSecrets["post_1"] = "do-not-leak"
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.SessionSecret != state.SessionSecret || loaded.DeleteSecrets["post_1"] != "do-not-leak" {
		t.Fatalf("state=%#v err=%v", loaded, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %o", info.Mode().Perm())
		}
	}
	if err := os.WriteFile(path, []byte(`{"session_secret":"do-not-leak"`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load()
	if err != nil || loaded.Version != 0 {
		t.Fatalf("recovery=%#v err=%v", loaded, err)
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 || strings.Contains(state.String(), state.SessionSecret) {
		t.Fatal("corrupt backup/redaction failure")
	}
	var log bytes.Buffer
	slog.New(slog.NewTextHandler(&log, nil)).Info("state", "value", state)
	for _, rendered := range []string{fmt.Sprintf("%v", state), fmt.Sprintf("%+v", state), fmt.Sprintf("%#v", state), log.String()} {
		if strings.Contains(rendered, state.SessionSecret) || strings.Contains(rendered, "do-not-leak") {
			t.Fatalf("state rendering leaked a secret: %q", rendered)
		}
	}
}

func TestCacheVerifiesSHA256(t *testing.T) {
	cache := NewCache(t.TempDir())
	body := []byte("normalized image")
	sum := sha256.Sum256(body)
	path, err := cache.Put("attachment_1", hex.EncodeToString(sum[:]), body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(body) {
		t.Fatal("cache contents")
	}
	if _, err := cache.Put("attachment_1", strings.Repeat("0", 64), body); err == nil {
		t.Fatal("expected checksum error")
	}
	if _, err := cache.Put("attachment_1", "not-a-digest", body); err == nil {
		t.Fatal("expected invalid checksum error")
	}
}

func TestOpenerCommandDoesNotUseShell(t *testing.T) {
	name, args := openerCommand("/tmp/image;touch PWNED.png")
	if name == "sh" || name == "bash" || name == "cmd.exe" {
		t.Fatalf("shell opener: %s", name)
	}
	if len(args) == 0 || args[len(args)-1] != "/tmp/image;touch PWNED.png" {
		t.Fatalf("args = %#v", args)
	}
}
