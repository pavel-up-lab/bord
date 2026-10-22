package adminclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminStateIsAtomicPrivateAndContainsNoCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "admin.json")
	store := NewStateStore(path)
	first := State{Management: "http://127.0.0.1:8081", Theme: "classic", View: "reports", InlineMode: "auto"}
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	second := State{Management: "http://127.0.0.1:9081", Theme: "green", View: "audit", InlineMode: "off"}
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Management != second.Management || loaded.Theme != second.Theme || loaded.View != second.View {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state mode=%o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"credential", "secret", "bearer", "token"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("serialized state contains %q: %s", forbidden, text)
		}
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".private-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files=%v err=%v", matches, err)
	}
}

func TestMissingAdminStateIsEmpty(t *testing.T) {
	state, err := NewStateStore(filepath.Join(t.TempDir(), "missing.json")).Load()
	if err != nil || state != (State{}) {
		t.Fatalf("state=%#v err=%v", state, err)
	}
}

func TestAdminStateRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	link := filepath.Join(directory, "admin.json")
	if err := os.WriteFile(target, []byte(`{"version":1,"management":"http://private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStateStore(link).Load(); err == nil {
		t.Fatal("state symlink accepted")
	}
}
