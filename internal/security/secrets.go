package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const SecretSize = 32

var ErrInvalidVerifier = errors.New("invalid verifier")
var ErrInvalidSecret = errors.New("invalid secret")

func GenerateSecret(random io.Reader) ([]byte, error) {
	secret := make([]byte, SecretSize)
	if _, err := io.ReadFull(random, secret); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	return secret, nil
}

func GenerateSessionSecret(random io.Reader) ([]byte, error) { return GenerateSecret(random) }
func GenerateDeleteSecret(random io.Reader) ([]byte, error)  { return GenerateSecret(random) }

func SessionFingerprint(secret []byte) ([32]byte, error) {
	if len(secret) != SecretSize {
		return [32]byte{}, ErrInvalidSecret
	}
	h := sha256.New()
	h.Write([]byte("bord/session-fingerprint/v1\x00"))
	h.Write(secret)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}

func ThreadAuthorID(key []byte, fingerprint [32]byte, threadID string) (string, error) {
	if len(key) < SecretSize || threadID == "" {
		return "", ErrInvalidSecret
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte("bord/thread-author/v1\x00"))
	h.Write(fingerprint[:])
	h.Write([]byte{0})
	h.Write([]byte(threadID))
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h.Sum(nil)[:12])), nil
}

type Argon2Params struct {
	Version     uint32
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

var DefaultArgon2Params = Argon2Params{Version: 1, Memory: 64 * 1024, Iterations: 3, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func (p Argon2Params) validate() error {
	if p.Version == 0 || p.Memory < 8 || p.Memory > 256*1024 || p.Iterations == 0 || p.Iterations > 10 || p.Parallelism == 0 || p.Parallelism > 16 || p.SaltLength < 8 || p.SaltLength > 64 || p.KeyLength < 16 || p.KeyLength > 64 {
		return ErrInvalidVerifier
	}
	return nil
}

func HashSecret(secret []byte, p Argon2Params, random io.Reader) (string, error) {
	if len(secret) < 16 || p.validate() != nil {
		return "", ErrInvalidVerifier
	}
	salt := make([]byte, p.SaltLength)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("generate verifier salt: %w", err)
	}
	digest := argon2.IDKey(secret, salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$bord-argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", p.Version, p.Memory, p.Iterations, p.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(digest)), nil
}

func VerifySecret(secret []byte, encoded string) bool {
	p, salt, expected, err := parseVerifier(encoded)
	if err != nil {
		return false
	}
	actual := argon2.IDKey(secret, salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

// ValidateVerifierParameters reports whether an encoded verifier uses exactly
// the server-approved Argon2id cost and output parameters. Public clients
// supply delete verifiers, so accepting arbitrary valid parameters would let a
// client persist a verifier that forces excessive CPU or memory use later.
func ValidateVerifierParameters(encoded string, approved Argon2Params) bool {
	p, _, _, err := parseVerifier(encoded)
	return err == nil && approved.validate() == nil && p == approved
}

func parseVerifier(encoded string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "bord-argon2id" || !strings.HasPrefix(parts[2], "v=") {
		return Argon2Params{}, nil, nil, ErrInvalidVerifier
	}
	version, err := strconv.ParseUint(strings.TrimPrefix(parts[2], "v="), 10, 32)
	settings := strings.Split(parts[3], ",")
	if err != nil || len(settings) != 3 || !strings.HasPrefix(settings[0], "m=") || !strings.HasPrefix(settings[1], "t=") || !strings.HasPrefix(settings[2], "p=") {
		return Argon2Params{}, nil, nil, ErrInvalidVerifier
	}
	memory, memoryErr := strconv.ParseUint(strings.TrimPrefix(settings[0], "m="), 10, 32)
	iterations, iterationErr := strconv.ParseUint(strings.TrimPrefix(settings[1], "t="), 10, 32)
	parallelism, parallelismErr := strconv.ParseUint(strings.TrimPrefix(settings[2], "p="), 10, 8)
	if memoryErr != nil || iterationErr != nil || parallelismErr != nil {
		return Argon2Params{}, nil, nil, ErrInvalidVerifier
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return Argon2Params{}, nil, nil, ErrInvalidVerifier
	}
	digest, err := b64.DecodeString(parts[5])
	if err != nil {
		return Argon2Params{}, nil, nil, ErrInvalidVerifier
	}
	p := Argon2Params{Version: uint32(version), Memory: uint32(memory), Iterations: uint32(iterations), Parallelism: uint8(parallelism), SaltLength: uint32(len(salt)), KeyLength: uint32(len(digest))}
	if p.validate() != nil || p.Version != 1 {
		return Argon2Params{}, nil, nil, ErrInvalidVerifier
	}
	return p, salt, digest, nil
}
