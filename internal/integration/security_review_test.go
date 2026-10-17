package integration

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"testing"

	"bord/internal/security"
)

func TestDecodeVerifierRejectsAttackerSelectedArgonCost(t *testing.T) {
	b64 := base64.RawStdEncoding
	salt := b64.EncodeToString(bytes.Repeat([]byte("s"), int(security.DefaultArgon2Params.SaltLength)))
	digest := b64.EncodeToString(bytes.Repeat([]byte("d"), int(security.DefaultArgon2Params.KeyLength)))
	encoded := fmt.Sprintf("$bord-argon2id$v=1$m=%d,t=10,p=16$%s$%s", 256*1024, salt, digest)
	wire := base64.RawURLEncoding.EncodeToString([]byte(encoded))
	if _, err := decodeVerifier(&wire); err == nil {
		t.Fatal("HTTP adapter accepted attacker-selected Argon2 resource cost")
	}
}
