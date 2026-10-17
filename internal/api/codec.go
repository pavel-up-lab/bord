package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultPageLimit = 50

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, "application/json") {
		return errors.New("content-type must be application/json")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return domainErrorPayloadTooLarge(err)
		}
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func pageLimit(r *http.Request) (int, error) {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return defaultPageLimit, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 100 {
		return 0, errors.New("limit must be between 1 and 100")
	}
	return n, nil
}

func validResourceID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validIdempotencyKey(key string) bool {
	if len(key) < 16 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._~-", c)) {
			return false
		}
	}
	return true
}

type cursorPayload struct {
	Collection string `json:"collection"`
	Position   string `json:"position"`
	Expires    int64  `json:"expires"`
}

type CursorCodec struct {
	key []byte
	now func() time.Time
}

func NewCursorCodec(key []byte, now func() time.Time) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, errors.New("cursor signing key must be at least 32 bytes")
	}
	if now == nil {
		now = time.Now
	}
	return &CursorCodec{key: append([]byte(nil), key...), now: now}, nil
}

func (c *CursorCodec) Encode(collection, position string, expires time.Time) (string, error) {
	b, err := json.Marshal(cursorPayload{collection, position, expires.Unix()})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (c *CursorCodec) Decode(token, collection string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 1024 {
		return "", errors.New("invalid cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errors.New("invalid cursor")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("invalid cursor")
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(b)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", errors.New("invalid cursor")
	}
	var payload cursorPayload
	if json.Unmarshal(b, &payload) != nil || payload.Collection != collection || payload.Expires <= c.now().Unix() {
		return "", errors.New("invalid cursor")
	}
	return payload.Position, nil
}
