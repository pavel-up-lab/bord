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
	for _, path := range []string{"/api/v1/boards", "/api/v1/events", "/admin/v1/reports", "/healthz", "/readyz"} {
		if doc.Paths.Find(path) == nil {
			t.Errorf("required path %s is absent", path)
		}
	}
}
