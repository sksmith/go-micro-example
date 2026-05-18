package httpx_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sksmith/go-micro-example/internal/platform/httpx"
)

func TestHSTS(t *testing.T) {
	noop := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	tests := []struct {
		name   string
		opts   httpx.HSTSOptions
		prep   func(*http.Request)
		header string
	}{
		{
			name:   "plaintext request omits header",
			opts:   httpx.HSTSOptions{IncludeSubDomains: true},
			prep:   func(*http.Request) {},
			header: "",
		},
		{
			name: "direct TLS request emits default header",
			opts: httpx.HSTSOptions{IncludeSubDomains: true},
			prep: func(r *http.Request) {
				r.TLS = &tls.ConnectionState{}
			},
			header: "max-age=31536000; includeSubDomains",
		},
		{
			name: "X-Forwarded-Proto=https triggers header",
			opts: httpx.HSTSOptions{IncludeSubDomains: true, Preload: true},
			prep: func(r *http.Request) {
				r.Header.Set("X-Forwarded-Proto", "https")
			},
			header: "max-age=31536000; includeSubDomains; preload",
		},
		{
			name: "X-Forwarded-Proto=http does not trigger header",
			opts: httpx.HSTSOptions{},
			prep: func(r *http.Request) {
				r.Header.Set("X-Forwarded-Proto", "http")
			},
			header: "",
		},
		{
			name: "custom max-age overrides default",
			opts: httpx.HSTSOptions{MaxAgeSeconds: 60},
			prep: func(r *http.Request) {
				r.TLS = &tls.ConnectionState{}
			},
			header: "max-age=60",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
			tc.prep(req)
			rec := httptest.NewRecorder()

			httpx.HSTS(tc.opts)(noop).ServeHTTP(rec, req)

			got := rec.Header().Get("Strict-Transport-Security")
			if got != tc.header {
				t.Errorf("Strict-Transport-Security got=%q want=%q", got, tc.header)
			}
		})
	}
}
