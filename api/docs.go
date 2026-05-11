package api

import (
	_ "embed"
	"net/http"

	"github.com/swaggest/swgui/v5emb"
)

//go:embed openapi.yaml
var openAPISpec []byte

// OpenAPISpec returns the embedded OpenAPI spec (api/openapi.yaml).
// Exposed for tests and for downstream tooling that wants to inspect
// the shipped contract.
func OpenAPISpec() []byte {
	return append([]byte(nil), openAPISpec...)
}

// OpenAPIHandler serves the embedded api/openapi.yaml at /openapi.yaml.
// The route is mounted by ConfigureRouter only when cfg.Docs.Enabled
// is true (default on local/dev, recommended off in prod).
func OpenAPIHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec)
	}
}

// SwaggerUIHandler serves an offline-bundled Swagger UI at /docs,
// pointing at /openapi.yaml. swaggest/swgui/v5emb embeds Swagger UI
// 5.x so there's no CDN dependency.
func SwaggerUIHandler(title string) http.Handler {
	return v5emb.New(title, "/openapi.yaml", "/docs")
}
