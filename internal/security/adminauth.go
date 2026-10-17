package security

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")
var ErrRoleDenied = errors.New("role denied")

type Role uint8

const (
	RoleReports Role = iota + 1
	RoleModerator
	RoleAdministrator
)

func (r Role) Allows(required Role) bool {
	return subtle.ConstantTimeLessOrEq(int(required), int(r)) == 1
}

type AdminIdentity struct {
	ID        string
	Role      Role
	Verifier  string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

type AdminIdentityRepository interface {
	ByID(context.Context, string) (AdminIdentity, bool, error)
	Save(context.Context, AdminIdentity) error
}

type AdminAuthenticator struct {
	repository AdminIdentityRepository
	clock      Clock
	dummy      string
}

func NewAdminAuthenticator(repository AdminIdentityRepository, clock Clock, dummyVerifier string) *AdminAuthenticator {
	return &AdminAuthenticator{repository: repository, clock: clock, dummy: dummyVerifier}
}

// Authenticate expects the public identity id and secret separated by a dot.
// Unknown identities still execute Argon2 verification against a dummy hash.
func (a *AdminAuthenticator) Authenticate(ctx context.Context, bearer string, required Role) (AdminIdentity, error) {
	id, secret, ok := strings.Cut(bearer, ".")
	if !ok || id == "" || secret == "" {
		VerifySecret([]byte(bearer), a.dummy)
		return AdminIdentity{}, ErrUnauthorized
	}
	identity, found, err := a.repository.ByID(ctx, id)
	if err != nil {
		return AdminIdentity{}, err
	}
	verifier := a.dummy
	if found {
		verifier = identity.Verifier
	}
	verified := VerifySecret([]byte(secret), verifier)
	now := a.clock.Now()
	if !found || !verified || identity.RevokedAt != nil || !identity.ExpiresAt.After(now) {
		return AdminIdentity{}, ErrUnauthorized
	}
	if !identity.Role.Allows(required) {
		return AdminIdentity{}, ErrRoleDenied
	}
	return identity, nil
}

// NewAdminCredential generates a high-entropy bearer and its server-side verifier.
func NewAdminCredential(id string, role Role, expiresAt time.Time, randomSource interface{ Read([]byte) (int, error) }, params Argon2Params) (string, AdminIdentity, error) {
	if id == "" || role < RoleReports || role > RoleAdministrator || expiresAt.IsZero() {
		return "", AdminIdentity{}, ErrUnauthorized
	}
	if randomSource == nil {
		randomSource = rand.Reader
	}
	secret, err := GenerateSecret(randomSource)
	if err != nil {
		return "", AdminIdentity{}, err
	}
	encodedSecret := encodeURL(secret)
	verifier, err := HashSecret([]byte(encodedSecret), params, randomSource)
	if err != nil {
		return "", AdminIdentity{}, err
	}
	return id + "." + encodedSecret, AdminIdentity{ID: id, Role: role, Verifier: verifier, ExpiresAt: expiresAt}, nil
}

func encodeURL(raw []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	result := make([]byte, 0, (len(raw)*8+5)/6)
	var acc uint32
	var bits uint8
	for _, value := range raw {
		acc = acc<<8 | uint32(value)
		bits += 8
		for bits >= 6 {
			bits -= 6
			result = append(result, alphabet[(acc>>bits)&63])
		}
	}
	if bits > 0 {
		result = append(result, alphabet[(acc<<(6-bits))&63])
	}
	return string(result)
}
