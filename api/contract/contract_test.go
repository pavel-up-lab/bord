package contract

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestOpenAPIContractIsValid(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/healthz",
		"/readyz",
		"/api/v1/boards",
		"/api/v1/boards/{slug}/threads",
		"/api/v1/threads/{thread_id}",
		"/api/v1/threads/{thread_id}/posts",
		"/api/v1/posts/{post_id}",
		"/api/v1/posts/{post_id}/reports",
		"/api/v1/attachments",
		"/api/v1/attachments/{attachment_id}",
		"/api/v1/events",
		"/api/v1/pow/challenges",
		"/admin/v1/reports",
		"/admin/v1/posts/{post_id}/visibility",
		"/admin/v1/threads/{thread_id}/close",
		"/admin/v1/bans",
		"/admin/v1/bans/{ban_id}",
		"/admin/v1/posts/{post_id}/ip",
	} {
		if doc.Paths.Find(path) == nil {
			t.Errorf("required path %s is absent", path)
		}
	}
}
