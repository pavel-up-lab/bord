package security

import (
	"net/netip"

	"bord/internal/app/moderation"
)

// ModerationIPDecrypter adapts the crypto keyring to the inward-facing
// moderation port without making application code depend on this package.
type ModerationIPDecrypter struct{ Keyring IPKeyring }

func (d ModerationIPDecrypter) DecryptIP(envelope moderation.EncryptedIP) (netip.Addr, error) {
	return d.Keyring.Decrypt(IPEnvelope{
		Version: envelope.Version, KeyID: envelope.KeyID,
		Nonce: envelope.Nonce, Ciphertext: envelope.Ciphertext,
	})
}
