package api

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"bord/internal/domain"
)

type Config struct {
	Public          PublicService
	Media           MediaService
	Abuse           AbuseService
	Admin           AdminService
	Events          EventStream
	Readiness       []ReadinessProbe
	Logger          *slog.Logger
	CursorCodec     *CursorCodec
	TrustedProxies  []*net.IPNet
	MaxBodyBytes    int64
	MaxMediaBytes   int64
	RequestTimeout  time.Duration
	HeartbeatPeriod time.Duration
}

type Handler struct{ config Config }

func NewRouter(config Config) http.Handler {
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 1 << 20
	}
	if config.MaxMediaBytes <= 0 {
		config.MaxMediaBytes = 10 << 20
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 15 * time.Second
	}
	if config.HeartbeatPeriod <= 0 {
		config.HeartbeatPeriod = 15 * time.Second
	}
	h := &Handler{config: config}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /readyz", h.ready)
	mux.HandleFunc("GET /api/v1/boards", h.listBoards)
	mux.HandleFunc("GET /api/v1/boards/{slug}/threads", h.listThreads)
	mux.HandleFunc("POST /api/v1/boards/{slug}/threads", h.createThread)
	mux.HandleFunc("GET /api/v1/threads/{thread_id}", h.getThread)
	mux.HandleFunc("GET /api/v1/threads/{thread_id}/posts", h.listPosts)
	mux.HandleFunc("POST /api/v1/threads/{thread_id}/posts", h.createPost)
	mux.HandleFunc("DELETE /api/v1/posts/{post_id}", h.deletePost)
	mux.HandleFunc("POST /api/v1/posts/{post_id}/reports", h.reportPost)
	mux.HandleFunc("POST /api/v1/attachments", h.uploadAttachment)
	mux.HandleFunc("GET /api/v1/attachments/{attachment_id}", h.getAttachment)
	mux.HandleFunc("POST /api/v1/pow/challenges", h.createChallenge)
	mux.HandleFunc("GET /api/v1/events", h.streamEvents)
	if config.Admin != nil {
		mux.Handle("GET /admin/v1/reports", h.admin(http.HandlerFunc(h.adminListReports)))
		mux.Handle("PUT /admin/v1/posts/{post_id}/visibility", h.admin(http.HandlerFunc(h.adminSetVisibility)))
		mux.Handle("POST /admin/v1/threads/{thread_id}/close", h.admin(http.HandlerFunc(h.adminCloseThread)))
		mux.Handle("POST /admin/v1/bans", h.admin(http.HandlerFunc(h.adminCreateBan)))
		mux.Handle("DELETE /admin/v1/bans/{ban_id}", h.admin(http.HandlerFunc(h.adminRevokeBan)))
		mux.Handle("POST /admin/v1/posts/{post_id}/ip", h.admin(http.HandlerFunc(h.adminRevealIP)))
	}

	var regular http.Handler = timeoutMiddleware(config.RequestTimeout, mux)
	// SSE must not inherit the ordinary request timeout.
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/events" {
			mux.ServeHTTP(w, r)
			return
		}
		regular.ServeHTTP(w, r)
	})
	var result http.Handler = dispatch
	// Wrapped in reverse declaration order; this is the documented execution order.
	result = securityHeadersMiddleware(result)
	result = accessLogMiddleware(config.Logger, result)
	result = bodyLimitMiddleware(config.MaxBodyBytes, result)
	result = trustedProxyMiddleware(config.TrustedProxies, result)
	result = requestIDMiddleware(result)
	result = recoverMiddleware(config.Logger, result)
	return result
}

// NewPublicRouter guarantees that management routes cannot be reached on the public listener.
func NewPublicRouter(config Config) http.Handler {
	config.Admin = nil
	return NewRouter(config)
}

// NewManagementRouter serves health and admin APIs but no public content APIs.
func NewManagementRouter(config Config) http.Handler {
	full := NewRouter(config)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || len(r.URL.Path) >= 10 && r.URL.Path[:10] == "/admin/v1/" {
			full.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, response := MapError(err, RequestID(r.Context()))
	var retry interface{ RetryAfter() time.Duration }
	if errors.As(err, &retry) {
		seconds := int(retry.RetryAfter().Seconds())
		if seconds < 1 {
			seconds = 1
		}
		response.RetryAfter = seconds
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	writeJSON(w, status, response)
}

func validation(field string, cause error) error {
	return domain.NewError(domain.CodeValidation, field, cause)
}
func unavailable() error {
	return domain.NewError(domain.CodeServiceUnavailable, "", errors.New("adapter unavailable"))
}
func domainInternal() error {
	return domain.NewError(domain.CodeInternal, "", errors.New("internal error"))
}
