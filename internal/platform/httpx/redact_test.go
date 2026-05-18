package httpx_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
)

func TestRedactURIPreservesSafePath(t *testing.T) {
	u, _ := url.Parse("/api/v1/inventory?limit=10&offset=0")
	got := httpx.RedactURI(u)
	want := "/api/v1/inventory?limit=10&offset=0"
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestRedactURIScrubsSensitiveKeys(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"token", "/p?token=abc", "/p?token=%5BREDACTED%5D"},
		{"access_token", "/p?access_token=abc", "/p?access_token=%5BREDACTED%5D"},
		{"refresh_token", "/p?refresh_token=abc", "/p?refresh_token=%5BREDACTED%5D"},
		{"id_token", "/p?id_token=abc", "/p?id_token=%5BREDACTED%5D"},
		{"code", "/p?code=xyz", "/p?code=%5BREDACTED%5D"},
		{"password", "/p?password=hunter2", "/p?password=%5BREDACTED%5D"},
		{"api_key", "/p?api_key=k", "/p?api_key=%5BREDACTED%5D"},
		{"apikey alt spelling", "/p?apikey=k", "/p?apikey=%5BREDACTED%5D"},
		{"case insensitive", "/p?Token=abc", "/p?Token=%5BREDACTED%5D"},
		{"mixed sensitive + safe", "/p?token=abc&limit=10", "/p?limit=10&token=%5BREDACTED%5D"},
		{"multi-value", "/p?token=a&token=b", "/p?token=%5BREDACTED%5D&token=%5BREDACTED%5D"},
		{"no query", "/p", "/p"},
		{"empty path with query", "/?token=abc", "/?token=%5BREDACTED%5D"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.in)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.in, err)
			}
			got := httpx.RedactURI(u)
			if got != tt.want {
				t.Errorf("got=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestRedactURIPreservesNonSensitiveQueryValues(t *testing.T) {
	// A real payload mixed with a sensitive key: the non-sensitive
	// values must round-trip unchanged so log lines remain useful.
	u, _ := url.Parse("/api/v1/orders?status=paid&customer=alice&token=secret")
	got := httpx.RedactURI(u)
	if !strings.Contains(got, "status=paid") {
		t.Errorf("non-sensitive 'status' dropped: %q", got)
	}
	if !strings.Contains(got, "customer=alice") {
		t.Errorf("non-sensitive 'customer' dropped: %q", got)
	}
	if strings.Contains(got, "secret") {
		t.Errorf("sensitive 'token' value leaked: %q", got)
	}
}

func TestRedactURIHandlesNil(t *testing.T) {
	if got := httpx.RedactURI(nil); got != "" {
		t.Errorf("nil URL got=%q want empty", got)
	}
}

// TestLoggingMiddlewareDoesNotLeakTokenQueryValue is the acceptance-
// criterion check: a request hitting an endpoint with ?token=abc
// must NOT produce the literal string "abc" anywhere in the logged
// line. The middleware is exercised via httptest; logs are captured
// to a buffer by swapping zerolog's writer.
func TestLoggingMiddlewareDoesNotLeakTokenQueryValue(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	prevLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		log.Logger = prev
		zerolog.SetGlobalLevel(prevLevel)
	})
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	log.Logger = zerolog.New(&buf)

	const secret = "supersecret-token-value-12345"
	handler := httpx.Logging(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/whatever?token="+secret+"&limit=10", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logged := buf.String()
	if strings.Contains(logged, secret) {
		t.Errorf("token value leaked into logs:\n%s", logged)
	}
	if !strings.Contains(logged, "limit=10") {
		t.Errorf("non-sensitive query param dropped from logs:\n%s", logged)
	}
	if !strings.Contains(logged, "REDACTED") {
		t.Errorf("expected REDACTED marker in logs:\n%s", logged)
	}
}
