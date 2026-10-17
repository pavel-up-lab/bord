package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrecedenceAndSecretFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("http:\n  address: file:9000\nstorage:\n  database: from-file.db\n"), 0600); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(dir, "lookup")
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	if err := os.WriteFile(secretPath, []byte(encoded+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"BORD_HTTP_ADDRESS": "env:9001", "BORD_IP_LOOKUP_KEY_FILE": secretPath}
	c, err := Load(configPath, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.Address != "env:9001" || c.Storage.Database != "from-file.db" || c.Storage.Blobs != "data/blobs" {
		t.Fatalf("bad precedence: %#v", c)
	}
	if string(c.Secrets.IPLookupKey) != strings.Repeat("k", 32) {
		t.Fatal("secret file not loaded")
	}
}

func TestEncryptionKeyRequired(t *testing.T) {
	lookup := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	env := map[string]string{"BORD_IP_LOOKUP_KEY": lookup, "BORD_STORE_RAW_IP": "true"}
	_, err := Load("", func(k string) string { return env[k] })
	if err == nil || !strings.Contains(err.Error(), "encryption key") {
		t.Fatalf("unexpected error: %v", err)
	}
}
