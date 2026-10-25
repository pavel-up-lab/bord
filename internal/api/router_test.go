package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bord/internal/domain"
	"bord/internal/security"
)

type publicFake struct {
	boards      []Board
	createCalls int
	panicList   bool
}

func (f *publicFake) ListBoards(context.Context) ([]Board, error) {
	if f.panicList {
		panic("secret panic details")
	}
	return f.boards, nil
}
func (*publicFake) ListThreads(context.Context, string, string, int) (Page[Thread], error) {
	return Page[Thread]{}, nil
}
func (*publicFake) GetThread(context.Context, string) (Thread, error) { return Thread{}, nil }
func (*publicFake) ListPosts(context.Context, string, string, int) (Page[Post], error) {
	return Page[Post]{}, nil
}
func (f *publicFake) CreateThread(context.Context, string, string, CreateContent) (CreateResult, error) {
	f.createCalls++
	return CreateResult{ThreadID: "thread_123", PostID: "post_1234"}, nil
}
func (f *publicFake) CreatePost(context.Context, string, string, CreateContent) (CreateResult, error) {
	f.createCalls++
	return CreateResult{ThreadID: "thread_123", PostID: "post_1234"}, nil
}
func (*publicFake) DeletePost(context.Context, string, string) error        { return nil }
func (*publicFake) ReportPost(context.Context, string, ReportRequest) error { return nil }

type mediaFake struct{ data string }

func (*mediaFake) Upload(context.Context, string, io.Reader) (Attachment, error) {
	return Attachment{}, nil
}

type adminFake struct {
	credential, requestID string
	boardErr              error
	role                  security.Role
}

type extendedAbuseFake struct {
	readOperation, writeOperation, session string
}

func (*extendedAbuseFake) Check(context.Context, string, string) error { return nil }
func (*extendedAbuseFake) Challenge(context.Context, string) (Challenge, error) {
	return Challenge{}, nil
}
func (a *extendedAbuseFake) CheckRead(_ context.Context, operation string) error {
	a.readOperation = operation
	return nil
}
func (a *extendedAbuseFake) CheckSession(_ context.Context, operation, _, session string) error {
	a.writeOperation, a.session = operation, session
	return nil
}

func (a *adminFake) Authenticate(_ context.Context, credential, requestID string) (AdminPrincipal, error) {
	a.credential, a.requestID = credential, requestID
	role := a.role
	if !role.Valid() {
		role = security.RoleAdministrator
	}
	return AdminPrincipal{ID: "test-admin", Role: role, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*adminFake) ListReports(context.Context, string, int) (Page[AdminReport], error) {
	return Page[AdminReport]{Items: []AdminReport{}}, nil
}
func (a *adminFake) ListBoards(context.Context, string, string, int) (Page[AdminBoard], error) {
	return Page[AdminBoard]{Items: []AdminBoard{}}, a.boardErr
}
func (a *adminFake) CreateBoard(context.Context, AdminCreateBoardRequest, string) (AdminBoard, error) {
	return AdminBoard{}, a.boardErr
}
func (a *adminFake) ArchiveBoard(context.Context, string, string, string) (AdminBoard, error) {
	return AdminBoard{}, a.boardErr
}
func (a *adminFake) RestoreBoard(context.Context, string, string, string) (AdminBoard, error) {
	return AdminBoard{}, a.boardErr
}
func (*adminFake) GetPostContext(context.Context, string, string, int) (AdminPostContext, error) {
	return AdminPostContext{Posts: []Post{}}, nil
}
func (*adminFake) OpenAttachment(context.Context, string) (io.ReadCloser, Attachment, error) {
	return io.NopCloser(strings.NewReader("png")), Attachment{ID: "attach_1", MediaType: "image/png", Size: 3}, nil
}
func (*adminFake) ListBans(context.Context, string, string, int) (Page[AdminBan], error) {
	return Page[AdminBan]{Items: []AdminBan{}}, nil
}
func (*adminFake) ListAudit(context.Context, string, int) (Page[AdminAuditEntry], error) {
	return Page[AdminAuditEntry]{Items: []AdminAuditEntry{{ID: "audit_01", Action: "post.visibility", Outcome: "failure", RequestID: "request-123", CreatedAt: time.Now()}}}, nil
}
func (*adminFake) CreatePostBan(context.Context, string, PostBanRequest, string) (Ban, error) {
	return Ban{ID: "ban_0002", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*adminFake) SetPostVisibility(context.Context, string, bool, string, string) error { return nil }
func (*adminFake) CloseThread(context.Context, string, string, string) error             { return nil }
func (*adminFake) CreateBan(context.Context, BanRequest, string) (Ban, error)            { return Ban{}, nil }
func (*adminFake) RevokeBan(context.Context, string, string, string) error               { return nil }
func (*adminFake) RevealPostIP(context.Context, string, string, string) (string, error) {
	return "192.0.2.1", nil
}
func (m *mediaFake) Open(context.Context, string) (io.ReadCloser, Attachment, error) {
	return io.NopCloser(strings.NewReader(m.data)), Attachment{ID: "attach_1", MediaType: "image/png", Filename: "../bad\"\nname.png", Size: int64(len(m.data))}, nil
}

func testRouter(t *testing.T, service PublicService) http.Handler {
	t.Helper()
	codec, err := NewCursorCodec(bytes.Repeat([]byte("k"), 32), func() time.Time { return time.Unix(100, 0) })
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(Config{Public: service, CursorCodec: codec})
}

func TestStrictJSONAndIdempotencyValidation(t *testing.T) {
	fake := &publicFake{}
	router := testRouter(t, fake)
	tests := []struct{ name, key, body string }{
		{"unknown field", strings.Repeat("a", 16), `{"body":"hello","extra":true}`},
		{"short key", "short", `{"body":"hello"}`},
		{"empty content", strings.Repeat("a", 16), `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/boards/test/threads", strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", test.key)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"validation_failed"`) {
				t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
			}
		})
	}
	if fake.createCalls != 0 {
		t.Fatalf("invalid requests reached service %d times", fake.createCalls)
	}
}

func TestFrozenAbusePortOptionalReadAndSessionDimensions(t *testing.T) {
	public := &publicFake{}
	abuse := &extendedAbuseFake{}
	router := NewRouter(Config{Public: public, Abuse: abuse})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/boards", nil))
	if w.Code != http.StatusOK || abuse.readOperation != "list_boards" {
		t.Fatalf("read adapter operation=%q status=%d", abuse.readOperation, w.Code)
	}
	proof := strings.Repeat("p", 32)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/boards/test/threads", strings.NewReader(`{"body":"hello","session_proof":"`+proof+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", strings.Repeat("a", 16))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated || abuse.writeOperation != "create_thread" || abuse.session != proof {
		t.Fatalf("session adapter operation=%q session=%q status=%d", abuse.writeOperation, abuse.session, w.Code)
	}
}

func TestStrictJSONMediaTypeAndCreateFieldValidation(t *testing.T) {
	fake := &publicFake{}
	router := testRouter(t, fake)
	tests := []struct {
		name        string
		path        string
		contentType string
		body        string
	}{
		{"missing content type", "/api/v1/boards/test/threads", "", `{"body":"hello"}`},
		{"prefix content type", "/api/v1/boards/test/threads", "application/json-patch+json", `{"body":"hello"}`},
		{"invalid attachment", "/api/v1/boards/test/threads", "application/json", `{"attachment_id":"short"}`},
		{"short session proof", "/api/v1/boards/test/threads", "application/json", `{"body":"hello","session_proof":"short"}`},
		{"short delete verifier", "/api/v1/boards/test/threads", "application/json", `{"body":"hello","delete_verifier":"short"}`},
		{"thread reply target", "/api/v1/boards/test/threads", "application/json", `{"body":"hello","reply_to_id":"post_1234"}`},
		{"invalid post reply target", "/api/v1/threads/thread_1/posts", "application/json", `{"body":"hello","reply_to_id":"short"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			req.Header.Set("Content-Type", test.contentType)
			req.Header.Set("Idempotency-Key", strings.Repeat("a", 16))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if fake.createCalls != 0 {
		t.Fatalf("invalid requests reached service %d times", fake.createCalls)
	}
}

func TestTrustedProxyUsesNearestUntrustedHop(t *testing.T) {
	_, trusted, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	handler := trustedProxyMiddleware([]*net.IPNet{trusted}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, ClientIP(r.Context()))
	}))
	tests := []struct {
		name, remote, forwarded, want string
	}{
		{"untrusted peer ignores header", "192.0.2.10:1234", "198.51.100.1", "192.0.2.10"},
		{"spoofed leftmost ignored", "10.0.0.3:1234", "203.0.113.99, 198.51.100.7", "198.51.100.7"},
		{"trusted chain traversed", "10.0.0.3:1234", "198.51.100.7, 10.0.0.2", "198.51.100.7"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = test.remote
			req.Header.Set("X-Forwarded-For", test.forwarded)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if got := response.Body.String(); got != test.want {
				t.Fatalf("client IP=%q want %q", got, test.want)
			}
		})
	}
}

func TestCursorIsSignedBoundAndExpiring(t *testing.T) {
	now := time.Unix(100, 0)
	codec, _ := NewCursorCodec(bytes.Repeat([]byte("s"), 32), func() time.Time { return now })
	token, err := codec.Encode("threads:test", "position", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := codec.Decode(token, "threads:test"); err != nil || got != "position" {
		t.Fatalf("decode = %q, %v", got, err)
	}
	if _, err := codec.Decode(token, "posts:id"); err == nil {
		t.Fatal("cursor accepted for another collection")
	}
	if _, err := codec.Decode(token+"x", "threads:test"); err == nil {
		t.Fatal("tampered cursor accepted")
	}
	// A signature made directly with the deployment root key must not verify;
	// cursor signing uses a derived, purpose-specific key.
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"collection":"threads:test","position":"position","expires":160}`))
	rootMAC := hmac.New(sha256.New, bytes.Repeat([]byte("s"), 32))
	decodedPayload, _ := base64.RawURLEncoding.DecodeString(payload)
	_, _ = rootMAC.Write(decodedPayload)
	forged := payload + "." + base64.RawURLEncoding.EncodeToString(rootMAC.Sum(nil))
	if _, err := codec.Decode(forged, "threads:test"); err == nil {
		t.Fatal("cursor signed with undifferentiated root key accepted")
	}
	now = now.Add(2 * time.Minute)
	if _, err := codec.Decode(token, "threads:test"); err == nil {
		t.Fatal("expired cursor accepted")
	}
}

func TestUniformErrorAndRetryAfter(t *testing.T) {
	err := &RetryError{Err: domain.NewError(domain.CodeRateLimited, "", errors.New("limited")), After: 1500 * time.Millisecond}
	status, response := MapError(err, "request-123")
	if status != http.StatusTooManyRequests || response.Code != domain.CodeRateLimited {
		t.Fatalf("unexpected mapping: %d %#v", status, response)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(context.WithValue(context.Background(), requestIDKey, "request-123"))
	w := httptest.NewRecorder()
	writeError(w, r, err)
	if w.Header().Get("Retry-After") != "2" || !strings.Contains(w.Body.String(), `"retry_after":2`) {
		t.Fatalf("missing retry metadata: %#v %s", w.Header(), w.Body.String())
	}
}

func TestAttachmentHeadersAndSafeFilename(t *testing.T) {
	codec, _ := NewCursorCodec(bytes.Repeat([]byte("k"), 32), time.Now)
	router := NewRouter(Config{Media: &mediaFake{data: "png"}, CursorCodec: codec})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/attachments/attach_1", nil))
	if w.Code != http.StatusOK || w.Header().Get("X-Content-Type-Options") != "nosniff" || strings.Contains(w.Header().Get("Content-Disposition"), "../") || strings.ContainsAny(w.Header().Get("Content-Disposition"), "\r\n") {
		t.Fatalf("unsafe response: %d %#v", w.Code, w.Header())
	}
}

func TestRecoveryHasRequestIDAndDoesNotLeakPanic(t *testing.T) {
	var logs bytes.Buffer
	router := NewRouter(Config{Public: &publicFake{panicList: true}, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/boards", nil)
	req.Header.Set("Authorization", "Bearer top-secret")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), `"request_id":"`) {
		t.Fatalf("unexpected recovery response: %d %s", w.Code, w.Body.String())
	}
	combined := logs.String() + w.Body.String()
	if strings.Contains(combined, "top-secret") || strings.Contains(combined, "secret panic details") {
		t.Fatalf("secret leaked: %s", combined)
	}
}

func TestManagementListenerPolicyAndAuditCorrelation(t *testing.T) {
	admin := &adminFake{}
	codec, _ := NewCursorCodec(bytes.Repeat([]byte("k"), 32), time.Now)
	_, trusted, _ := net.ParseCIDR("192.0.2.0/24")
	config := Config{Admin: admin, CursorCodec: codec, TrustedProxies: []*net.IPNet{trusted}}
	public := NewPublicRouter(config)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/admin/v1/reports"},
		{http.MethodGet, "/admin/v1/session"},
		{http.MethodGet, "/admin/v1/boards"},
		{http.MethodPost, "/admin/v1/boards"},
		{http.MethodPost, "/admin/v1/boards/board_01/archive"},
		{http.MethodPost, "/admin/v1/boards/board_01/restore"},
		{http.MethodGet, "/admin/v1/posts/post_001/context"},
		{http.MethodGet, "/admin/v1/attachments/attach_1"},
		{http.MethodPut, "/admin/v1/posts/post_001/visibility"},
		{http.MethodPost, "/admin/v1/threads/thread_1/close"},
		{http.MethodGet, "/admin/v1/bans"},
		{http.MethodPost, "/admin/v1/bans"},
		{http.MethodDelete, "/admin/v1/bans/ban_0001"},
		{http.MethodGet, "/admin/v1/audit"},
		{http.MethodPost, "/admin/v1/posts/post_001/ip"},
		{http.MethodPost, "/admin/v1/posts/post_001/bans"},
	} {
		w := httptest.NewRecorder()
		public.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("admin route %s %s exposed publicly: %d", route.method, route.path, w.Code)
		}
	}
	management := NewManagementRouter(config)
	w := httptest.NewRecorder()
	management.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/boards", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("public route exposed on management listener: %d", w.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/reports", nil)
	req.Header.Set("Authorization", "bearer credential-value")
	req.Header.Set("X-Request-ID", "request-123")
	w = httptest.NewRecorder()
	management.ServeHTTP(w, req)
	if w.Code != http.StatusOK || admin.credential != "credential-value" || admin.requestID != "request-123" {
		t.Fatalf("admin request not correlated: %d %#v", w.Code, admin)
	}
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/session", nil)
	req.Header.Set("Authorization", "Bearer credential-value")
	w = httptest.NewRecorder()
	management.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"test-admin"`) || !strings.Contains(w.Body.String(), `"role":"administrator"`) || strings.Contains(w.Body.String(), "credential-value") {
		t.Fatalf("unsafe session response: %d %s", w.Code, w.Body.String())
	}
	admin.boardErr = domain.NewError(domain.CodeForbidden, "", nil)
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/boards", nil)
	req.Header.Set("Authorization", "Bearer credential-value")
	w = httptest.NewRecorder()
	management.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("moderator-equivalent board denial status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRequestIDAcceptedOnlyFromTrustedEdge(t *testing.T) {
	_, trusted, _ := net.ParseCIDR("10.0.0.0/8")
	handler := requestIDMiddleware([]*net.IPNet{trusted}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, RequestID(r.Context()))
	}))
	for _, test := range []struct {
		remote, want string
		trusted      bool
	}{
		{remote: "192.0.2.1:1234", want: "attacker-request", trusted: false},
		{remote: "10.0.0.1:1234", want: "edge-request-123", trusted: true},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = test.remote
		req.Header.Set("X-Request-ID", test.want)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if test.trusted && w.Body.String() != test.want {
			t.Fatalf("trusted edge ID=%q", w.Body.String())
		}
		if !test.trusted && w.Body.String() == test.want {
			t.Fatal("untrusted request ID accepted")
		}
	}
}

func TestAdminReasonRejectsRawIPRepresentations(t *testing.T) {
	for _, reason := range []string{
		"source=192.0.2.1", "endpoint [2001:db8::1]:443", "network 198.51.100.0/24", "zone fe80::1%eth0",
	} {
		if validReason(reason) {
			t.Fatalf("unsafe reason accepted: %q", reason)
		}
	}
	if !validReason("repeated harassment from correlated source") {
		t.Fatal("safe reason rejected")
	}
}

type unavailableEventStream struct{}

func (unavailableEventStream) Subscribe(string, string, domain.EventID) ([]Event, bool, Subscription) {
	return nil, false, nil
}

func TestSSERejectsInvalidBoardScope(t *testing.T) {
	router := NewRouter(Config{Events: unavailableEventStream{}})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/events?board=Invalid", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSSEAcceptsGlobalScopeAndRejectsMixedScopes(t *testing.T) {
	router := NewRouter(Config{Events: unavailableEventStream{}})
	global := httptest.NewRecorder()
	router.ServeHTTP(global, httptest.NewRequest(http.MethodGet, "/api/v1/events?global=true", nil))
	if global.Code != http.StatusServiceUnavailable {
		t.Fatalf("global status=%d body=%s", global.Code, global.Body.String())
	}
	mixed := httptest.NewRecorder()
	router.ServeHTTP(mixed, httptest.NewRequest(http.MethodGet, "/api/v1/events?global=true&board=test", nil))
	if mixed.Code != http.StatusBadRequest {
		t.Fatalf("mixed status=%d body=%s", mixed.Code, mixed.Body.String())
	}
}

func TestHealthStaysUpWhenReadinessFails(t *testing.T) {
	router := NewRouter(Config{Readiness: []ReadinessProbe{ReadinessProbeFunc(func(context.Context) error { return errors.New("database down") })}})
	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if health.Code != http.StatusOK || ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("health=%d readiness=%d", health.Code, ready.Code)
	}
}

func TestAdminSystemStorageReportsConfiguredFilesystemToAdministrators(t *testing.T) {
	admin := &adminFake{}
	requestedPath := ""
	router := NewManagementRouter(Config{
		Admin: admin, DiskPath: "/srv/bord/data",
		DiskUsage: func(path string) (DiskSpace, error) {
			requestedPath = path
			return DiskSpace{TotalBytes: 40 << 30, AvailableBytes: 12 << 30}, nil
		},
	})
	request := httptest.NewRequest(http.MethodGet, "/admin/v1/system/storage", nil)
	request.Header.Set("Authorization", "Bearer credential-value")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || requestedPath != "/srv/bord/data" || !strings.Contains(response.Body.String(), `"available_bytes":12884901888`) || strings.Contains(response.Body.String(), "/srv/bord/data") {
		t.Fatalf("status=%d path=%q body=%s", response.Code, requestedPath, response.Body.String())
	}

	admin.role = security.RoleModerator
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("moderator storage status=%d body=%s", response.Code, response.Body.String())
	}
}
