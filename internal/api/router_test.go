package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bord/internal/domain"
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

type adminFake struct{ credential, requestID string }

func (a *adminFake) Authenticate(_ context.Context, credential, requestID string) error {
	a.credential, a.requestID = credential, requestID
	return nil
}
func (*adminFake) ListReports(context.Context, string, int) (Page[AdminReport], error) {
	return Page[AdminReport]{Items: []AdminReport{}}, nil
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
	if w.Header().Get("Retry-After") != "1" || !strings.Contains(w.Body.String(), `"retry_after":1`) {
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
	config := Config{Admin: admin, CursorCodec: codec}
	public := NewPublicRouter(config)
	w := httptest.NewRecorder()
	public.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/v1/reports", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("admin route exposed publicly: %d", w.Code)
	}
	management := NewManagementRouter(config)
	w = httptest.NewRecorder()
	management.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/boards", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("public route exposed on management listener: %d", w.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/reports", nil)
	req.Header.Set("Authorization", "Bearer credential-value")
	req.Header.Set("X-Request-ID", "request-123")
	w = httptest.NewRecorder()
	management.ServeHTTP(w, req)
	if w.Code != http.StatusOK || admin.credential != "credential-value" || admin.requestID != "request-123" {
		t.Fatalf("admin request not correlated: %d %#v", w.Code, admin)
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
