package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
)

var ErrInvalidIP = errors.New("invalid IP address")
var ErrUnknownKey = errors.New("unknown encryption key")

type ProxyResolver struct{ trusted []netip.Prefix }

func NewProxyResolver(prefixes []string) (ProxyResolver, error) {
	r := ProxyResolver{}
	for _, value := range prefixes {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return ProxyResolver{}, fmt.Errorf("trusted proxy %q: %w", value, err)
		}
		r.trusted = append(r.trusted, prefix.Masked())
	}
	return r, nil
}

func (r ProxyResolver) trustedAddr(addr netip.Addr) bool {
	for _, prefix := range r.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func parseRemoteAddr(remote string) (netip.Addr, error) {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(remote))
	if err != nil {
		return netip.Addr{}, ErrInvalidIP
	}
	return addr.WithZone("").Unmap(), nil
}

// Resolve ignores forwarded data unless the immediate peer is trusted. It walks
// the chain from the peer towards the client and returns the first untrusted hop.
func (r ProxyResolver) Resolve(remoteAddr, forwardedFor string) (netip.Addr, error) {
	peer, err := parseRemoteAddr(remoteAddr)
	if err != nil {
		return netip.Addr{}, err
	}
	if !r.trustedAddr(peer) || strings.TrimSpace(forwardedFor) == "" {
		return peer, nil
	}
	parts := strings.Split(forwardedFor, ",")
	chain := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		addr, err := parseRemoteAddr(strings.TrimSpace(part))
		if err != nil {
			return peer, nil
		}
		chain = append(chain, addr)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if !r.trustedAddr(chain[i]) {
			return chain[i], nil
		}
	}
	return chain[0], nil
}

func CanonicalIP(addr netip.Addr) ([16]byte, error) {
	if !addr.IsValid() {
		return [16]byte{}, ErrInvalidIP
	}
	return addr.Unmap().As16(), nil
}

func IPPrefix(addr netip.Addr) (netip.Prefix, error) {
	if !addr.IsValid() {
		return netip.Prefix{}, ErrInvalidIP
	}
	addr = addr.Unmap()
	bits := 64
	if addr.Is4() {
		bits = 24
	}
	return netip.PrefixFrom(addr, bits).Masked(), nil
}

// ContainsRawIP reports whether untrusted free text contains an IP address,
// prefix, or IP endpoint. It intentionally recognizes addresses next to common
// prose punctuation so audit callers cannot bypass the privacy check with
// forms such as "source=192.0.2.1" or "[2001:db8::1]:443".
func ContainsRawIP(value string) bool {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !strings.ContainsRune("0123456789abcdefABCDEF.:/%[]", r)
	})
	for _, field := range fields {
		candidate := strings.Trim(field, ".,;(){}<>\"'")
		if candidate == "" {
			continue
		}
		// Check substrings too. Adjacent hexadecimal prose (for example the
		// trailing 'e' in "case192.0.2.1") is otherwise indistinguishable
		// while tokenizing, and must not provide an audit bypass.
		for start := 0; start < len(candidate); start++ {
			for end := len(candidate); end > start; end-- {
				part := strings.Trim(candidate[start:end], ".,;(){}<>\"'")
				if rawIPCandidate(part) {
					return true
				}
			}
		}
	}
	return false
}

func rawIPCandidate(candidate string) bool {
	if addr, err := netip.ParseAddr(candidate); err == nil && addr.IsValid() {
		return true
	}
	if prefix, err := netip.ParsePrefix(candidate); err == nil && prefix.Addr().IsValid() {
		return true
	}
	if endpoint, err := netip.ParseAddrPort(candidate); err == nil && endpoint.Addr().IsValid() {
		return true
	}
	if base, _, ok := strings.Cut(candidate, "%"); ok {
		if addr, err := netip.ParseAddr(strings.Trim(base, "[]")); err == nil && addr.IsValid() {
			return true
		}
	}
	return false
}

func IPLookup(key []byte, addr netip.Addr) ([32]byte, error) {
	if len(key) < SecretSize {
		return [32]byte{}, ErrInvalidSecret
	}
	canonical, err := CanonicalIP(addr)
	if err != nil {
		return [32]byte{}, err
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte("bord/ip-lookup/v1\x00"))
	h.Write(canonical[:])
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}

type IPEnvelope struct {
	Version    uint8  `json:"version"`
	KeyID      string `json:"key_id"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type IPKeyring struct {
	Primary string
	Keys    map[string][]byte
}

func (k IPKeyring) Encrypt(addr netip.Addr, random io.Reader) (IPEnvelope, error) {
	key, ok := k.Keys[k.Primary]
	if !ok || len(key) != 32 {
		return IPEnvelope{}, ErrUnknownKey
	}
	canonical, err := CanonicalIP(addr)
	if err != nil {
		return IPEnvelope{}, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return IPEnvelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return IPEnvelope{}, fmt.Errorf("generate IP nonce: %w", err)
	}
	aad := []byte("bord/ip-envelope/v1\x00" + k.Primary)
	ciphertext := aead.Seal(nil, nonce, canonical[:], aad)
	b64 := base64.RawStdEncoding
	return IPEnvelope{Version: 1, KeyID: k.Primary, Nonce: b64.EncodeToString(nonce), Ciphertext: b64.EncodeToString(ciphertext)}, nil
}

func (k IPKeyring) Decrypt(envelope IPEnvelope) (netip.Addr, error) {
	if envelope.Version != 1 {
		return netip.Addr{}, errors.New("unsupported IP envelope")
	}
	key, ok := k.Keys[envelope.KeyID]
	if !ok || len(key) != 32 {
		return netip.Addr{}, ErrUnknownKey
	}
	aead, err := newGCM(key)
	if err != nil {
		return netip.Addr{}, err
	}
	b64 := base64.RawStdEncoding
	nonce, err := b64.DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != aead.NonceSize() {
		return netip.Addr{}, errors.New("invalid IP envelope")
	}
	ciphertext, err := b64.DecodeString(envelope.Ciphertext)
	if err != nil {
		return netip.Addr{}, errors.New("invalid IP envelope")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte("bord/ip-envelope/v1\x00"+envelope.KeyID))
	if err != nil || len(plaintext) != 16 {
		return netip.Addr{}, errors.New("invalid IP envelope")
	}
	var raw [16]byte
	copy(raw[:], plaintext)
	return netip.AddrFrom16(raw).Unmap(), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
