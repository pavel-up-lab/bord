package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"bord/internal/platform/requestid"
)

type contextKey uint8

const (
	requestIDKey contextKey = iota
	clientIPKey
)

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func ClientIP(ctx context.Context) string {
	value, _ := ctx.Value(clientIPKey).(string)
	return value
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func recoverMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				id := w.Header().Get("X-Request-ID")
				r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
				logger.ErrorContext(r.Context(), "request panic", "event", "http.panic", "request_id", id)
				writeError(w, r, domainInternal())
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestIDMiddleware(trusted []*net.IPNet, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ""
		// Forwarded correlation data is authoritative only at a configured edge.
		// Direct clients always receive a server-generated identifier.
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if trustedIP(net.ParseIP(host), trusted) {
			id = r.Header.Get("X-Request-ID")
		}
		if _, err := requestid.ParseRequest(id); err != nil {
			var b [16]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func trustedProxyMiddleware(trusted []*net.IPNet, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		peer := net.ParseIP(host)
		client := peer
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		// Walk from the socket peer toward the client. An untrusted hop is the
		// trust boundary; values to its left are client-controlled and ignored.
		for i := len(parts) - 1; i >= 0 && trustedIP(client, trusted); i-- {
			forwarded := net.ParseIP(strings.TrimSpace(parts[i]))
			if forwarded == nil {
				break
			}
			client = forwarded
		}
		value := ""
		if client != nil {
			value = client.String()
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey, value)))
	})
}

func trustedIP(ip net.IP, networks []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, network := range networks {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func bodyLimitMiddleware(limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && !(r.Method == http.MethodPost && r.URL.Path == "/api/v1/attachments") {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

func timeoutMiddleware(timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func accessLogMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		defer func() {
			recovered := recover()
			status := recorder.status
			if recovered != nil {
				status = http.StatusInternalServerError
			} else if status == 0 {
				status = http.StatusOK
			}
			logger.InfoContext(r.Context(), "HTTP request", "event", "http.request", "request_id", RequestID(r.Context()), "method", r.Method, "path", r.URL.Path, "status", status, "duration_ms", time.Since(started).Milliseconds())
			if recovered != nil {
				panic(recovered)
			}
		}()
		next.ServeHTTP(recorder, r)
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
