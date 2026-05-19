package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sksmith/go-micro-example/internal/app"
)

func TestOpenAPIYAMLServedWhenDocsEnabled(t *testing.T) {
	r, _ := newTestRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + app.OpenAPIEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want=200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Errorf("Content-Type=%q", ct)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "openapi:") {
		t.Errorf("spec body does not look like OpenAPI: %s", body[:min(120, len(body))])
	}
}

func TestSwaggerUIServedWhenDocsEnabled(t *testing.T) {
	r, _ := newTestRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + app.DocsEndpoint + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want=200", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "swagger-ui") {
		t.Errorf("expected swagger-ui markup, got %q", body[:min(200, len(body))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
