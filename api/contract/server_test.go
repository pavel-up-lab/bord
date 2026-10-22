package contract

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bord/api/generated"
	"bord/internal/api"
	"bord/internal/security"
)

type publicPort struct{}

func (publicPort) ListBoards(context.Context) ([]api.Board, error) {
	return []api.Board{{ID: "board_01", Slug: "test", Title: "Test"}}, nil
}
func (publicPort) ListThreads(context.Context, string, string, int) (api.Page[api.Thread], error) {
	return api.Page[api.Thread]{Items: []api.Thread{}}, nil
}
func (publicPort) GetThread(context.Context, string) (api.Thread, error) {
	return api.Thread{ID: "thread_1", BoardID: "board_01", CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}, nil
}
func (publicPort) ListPosts(context.Context, string, string, int) (api.Page[api.Post], error) {
	return api.Page[api.Post]{Items: []api.Post{}}, nil
}
func (publicPort) CreateThread(context.Context, string, string, api.CreateContent) (api.CreateResult, error) {
	return api.CreateResult{ThreadID: "thread_1", PostID: "post_001"}, nil
}
func (publicPort) CreatePost(context.Context, string, string, api.CreateContent) (api.CreateResult, error) {
	return api.CreateResult{ThreadID: "thread_1", PostID: "post_002"}, nil
}
func (publicPort) DeletePost(context.Context, string, string) error            { return nil }
func (publicPort) ReportPost(context.Context, string, api.ReportRequest) error { return nil }

type mediaPort struct{}

func (mediaPort) Upload(context.Context, string, io.Reader) (api.Attachment, error) {
	return attachment(), nil
}
func (mediaPort) Open(context.Context, string) (io.ReadCloser, api.Attachment, error) {
	return io.NopCloser(strings.NewReader("png")), attachment(), nil
}
func attachment() api.Attachment {
	return api.Attachment{ID: "attach_1", MediaType: "image/png", Size: 3, Width: 1, Height: 1, SHA256: strings.Repeat("a", 64)}
}

type abusePort struct{}

func (abusePort) Check(context.Context, string, string) error { return nil }
func (abusePort) Challenge(context.Context, string) (api.Challenge, error) {
	return api.Challenge{Token: "challenge", Difficulty: 1, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

type adminPort struct{}

func (adminPort) Authenticate(context.Context, string, string) (api.AdminPrincipal, error) {
	return api.AdminPrincipal{ID: "contract", Role: security.RoleAdministrator, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (adminPort) ListReports(context.Context, string, int) (api.Page[api.AdminReport], error) {
	return api.Page[api.AdminReport]{Items: []api.AdminReport{}}, nil
}
func (adminPort) ListBoards(context.Context, string, string, int) (api.Page[api.AdminBoard], error) {
	return api.Page[api.AdminBoard]{Items: []api.AdminBoard{}}, nil
}
func (adminPort) CreateBoard(context.Context, api.AdminCreateBoardRequest, string) (api.AdminBoard, error) {
	return api.AdminBoard{}, nil
}
func (adminPort) ArchiveBoard(context.Context, string, string, string) (api.AdminBoard, error) {
	return api.AdminBoard{}, nil
}
func (adminPort) RestoreBoard(context.Context, string, string, string) (api.AdminBoard, error) {
	return api.AdminBoard{}, nil
}
func (adminPort) GetPostContext(context.Context, string, string, int) (api.AdminPostContext, error) {
	return api.AdminPostContext{Board: api.AdminBoardSummary{ID: "board_01", Slug: "test", Title: "Test"}, Thread: api.AdminThreadSummary{ID: "thread_1"}, TargetPost: "post_001", Posts: []api.Post{}}, nil
}
func (adminPort) OpenAttachment(context.Context, string) (io.ReadCloser, api.Attachment, error) {
	return io.NopCloser(strings.NewReader("png")), attachment(), nil
}
func (adminPort) ListBans(context.Context, string, string, int) (api.Page[api.AdminBan], error) {
	return api.Page[api.AdminBan]{Items: []api.AdminBan{}}, nil
}
func (adminPort) ListAudit(context.Context, string, int) (api.Page[api.AdminAuditEntry], error) {
	return api.Page[api.AdminAuditEntry]{Items: []api.AdminAuditEntry{}}, nil
}
func (adminPort) CreatePostBan(context.Context, string, api.PostBanRequest, string) (api.Ban, error) {
	return api.Ban{ID: "ban_0002", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (adminPort) SetPostVisibility(context.Context, string, bool, string, string) error { return nil }
func (adminPort) CloseThread(context.Context, string, string, string) error             { return nil }
func (adminPort) CreateBan(context.Context, api.BanRequest, string) (api.Ban, error) {
	return api.Ban{ID: "ban_0001", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (adminPort) RevokeBan(context.Context, string, string, string) error { return nil }
func (adminPort) RevealPostIP(context.Context, string, string, string) (string, error) {
	return "192.0.2.1", nil
}

func TestGeneratedClientAgainstHTTPAdapter(t *testing.T) {
	server := httptest.NewServer(api.NewRouter(api.Config{Public: publicPort{}, Media: mediaPort{}, Abuse: abusePort{}, Admin: adminPort{}}))
	defer server.Close()
	client, err := generated.NewClientWithResponses(server.URL, generated.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer contract-credential")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.HealthWithResponse(context.Background())
	if err != nil || health.JSON200 == nil || health.StatusCode() != 200 {
		t.Fatalf("health response: %#v, %v", health, err)
	}
	boards, err := client.ListBoardsWithResponse(context.Background())
	if err != nil || boards.JSON200 == nil || len(boards.JSON200.Boards) != 1 || boards.JSON200.Boards[0].Slug != "test" {
		t.Fatalf("boards response: %#v, %v", boards, err)
	}
	body := "hello"
	created, err := client.CreateThreadWithResponse(context.Background(), "test", &generated.CreateThreadParams{IdempotencyKey: strings.Repeat("k", 16)}, generated.CreateThread{Body: &body})
	if err != nil || created.JSON201 == nil || created.JSON201.PostId != "post_001" {
		t.Fatalf("create response: %#v, %v", created, err)
	}
	assertCall(t, func() (*generated.ListThreadsResponse, error) {
		return client.ListThreadsWithResponse(context.Background(), "test", &generated.ListThreadsParams{})
	}, 200)
	assertCall(t, func() (*generated.GetThreadResponse, error) {
		return client.GetThreadWithResponse(context.Background(), "thread_1")
	}, 200)
	assertCall(t, func() (*generated.ListPostsResponse, error) {
		return client.ListPostsWithResponse(context.Background(), "thread_1", &generated.ListPostsParams{})
	}, 200)
	assertCall(t, func() (*generated.CreatePostResponse, error) {
		return client.CreatePostWithResponse(context.Background(), "thread_1", &generated.CreatePostParams{IdempotencyKey: strings.Repeat("p", 16)}, generated.CreatePost{Body: &body})
	}, 201)
	assertCall(t, func() (*generated.DeletePostResponse, error) {
		return client.DeletePostWithResponse(context.Background(), "post_001", generated.DeletePost{DeleteToken: strings.Repeat("d", 32)})
	}, 204)
	assertCall(t, func() (*generated.ReportPostResponse, error) {
		return client.ReportPostWithResponse(context.Background(), "post_001", generated.CreateReport{Reason: generated.Spam})
	}, 202)
	assertCall(t, func() (*generated.UploadAttachmentResponse, error) {
		return client.UploadAttachmentWithBodyWithResponse(context.Background(), "image/png", strings.NewReader("png"))
	}, 201)
	assertCall(t, func() (*generated.GetAttachmentResponse, error) {
		return client.GetAttachmentWithResponse(context.Background(), "attach_1")
	}, 200)
	assertCall(t, func() (*generated.CreateChallengeResponse, error) {
		return client.CreateChallengeWithResponse(context.Background(), generated.ChallengeRequest{Purpose: generated.ChallengeRequestPurposeCreatePost})
	}, 200)
	assertCall(t, func() (*generated.ReadinessResponse, error) {
		return client.ReadinessWithResponse(context.Background())
	}, 200)
	assertCall(t, func() (*generated.AdminListReportsResponse, error) {
		return client.AdminListReportsWithResponse(context.Background(), &generated.AdminListReportsParams{})
	}, 200)
	assertCall(t, func() (*generated.AdminGetSessionResponse, error) {
		return client.AdminGetSessionWithResponse(context.Background())
	}, 200)
	assertCall(t, func() (*generated.AdminGetPostContextResponse, error) {
		return client.AdminGetPostContextWithResponse(context.Background(), "post_001", &generated.AdminGetPostContextParams{})
	}, 200)
	assertCall(t, func() (*generated.AdminGetAttachmentResponse, error) {
		return client.AdminGetAttachmentWithResponse(context.Background(), "attach_1")
	}, 200)
	assertCall(t, func() (*generated.AdminListBansResponse, error) {
		return client.AdminListBansWithResponse(context.Background(), &generated.AdminListBansParams{})
	}, 200)
	assertCall(t, func() (*generated.AdminListAuditResponse, error) {
		return client.AdminListAuditWithResponse(context.Background(), &generated.AdminListAuditParams{})
	}, 200)
	assertCall(t, func() (*generated.AdminCreatePostBanResponse, error) {
		return client.AdminCreatePostBanWithResponse(context.Background(), "post_001", generated.CreatePostBan{Operations: []generated.CreatePostBanOperations{generated.CreatePostBanOperationsCreatePost}, ExpiresAt: time.Now().Add(time.Hour), Reason: "contract reason"})
	}, 201)
	assertCall(t, func() (*generated.AdminSetPostVisibilityResponse, error) {
		return client.AdminSetPostVisibilityWithResponse(context.Background(), "post_001", generated.SetVisibility{Visible: false, Reason: "contract reason"})
	}, 204)
	assertCall(t, func() (*generated.AdminCloseThreadResponse, error) {
		return client.AdminCloseThreadWithResponse(context.Background(), "thread_1", generated.Reason{Reason: "contract reason"})
	}, 204)
	assertCall(t, func() (*generated.AdminCreateBanResponse, error) {
		return client.AdminCreateBanWithResponse(context.Background(), generated.CreateBan{SubjectType: generated.CreateBanSubjectTypeLookup, Subject: "subject", Operations: []string{"write"}, ExpiresAt: time.Now().Add(time.Hour), Reason: "contract reason"})
	}, 201)
	assertCall(t, func() (*generated.AdminRevokeBanResponse, error) {
		return client.AdminRevokeBanWithResponse(context.Background(), "ban_0001", generated.Reason{Reason: "contract reason"})
	}, 204)
	assertCall(t, func() (*generated.AdminRevealPostIPResponse, error) {
		return client.AdminRevealPostIPWithResponse(context.Background(), "post_001", generated.Reason{Reason: "contract reason"})
	}, 200)
}

type statusResponse interface{ StatusCode() int }

func assertCall[T statusResponse](t *testing.T, call func() (T, error), want int) {
	t.Helper()
	response, err := call()
	if err != nil || response.StatusCode() != want {
		t.Fatalf("status=%d want=%d err=%v", response.StatusCode(), want, err)
	}
}
