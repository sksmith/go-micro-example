package web

import (
	"bytes"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGolden refreshes the testdata/*.golden files when set. Wire
// into `make ui-snapshots` so a UI tweak regenerates the goldens via
// `go test ./internal/web -update`.
var updateGolden = flag.Bool("update", false, "rewrite testdata golden files instead of comparing")

func TestIndexRender_GoldenShape(t *testing.T) {
	tmpl := mustParseTemplates()
	page := IndexPage{
		Title:       "operator console",
		Description: "Every input/output surface, exercised end-to-end.",
		ServerTime:  "2026-05-20 00:00:00 UTC",
		AuthState:   "locked",
		Year:        2026,
		RunID:       "r-fixed-id",
		Cards:       capabilityCards(),
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.gohtml", page); err != nil {
		t.Fatalf("execute index: %v", err)
	}

	// Sanity-check key contract bits before the golden diff — these
	// assertions tell the reader what we mean by "rendered" without
	// requiring them to diff the .golden file by eye.
	got := buf.String()
	for _, want := range []string{
		`<!doctype html>`,
		`/ui/static/ui.css`,
		`/ui/static/htmx.min.js`,
		`data-card="rest-create"`,
		`data-card="kafka-publish"`,
		`data-card="rabbitmq"`,
		`hx-post="/ui/cards/rest-create/try"`,
		`scope-body`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("index.gohtml missing %q", want)
		}
	}

	if c := strings.Count(got, `class="card`); c < 9 {
		t.Errorf("expected at least 9 capability cards rendered, got %d", c)
	}

	checkGolden(t, "index.golden.html", buf.Bytes())
}

func TestScopeRender_StatusKinds(t *testing.T) {
	tmpl := mustParseTemplates()

	cases := []struct {
		name string
		view ScopeView
		want []string
	}{
		{
			name: "ok",
			view: ScopeView{
				Card: "REST create", Method: "POST", URL: "http://localhost/api/v1/inventory",
				Status: 200, StatusText: "OK", StatusKind: "ok",
				ResponseBody: `{"sku":"DEMO-SKU-001"}`,
				LatencyMs:    42, RequestID: "req-abc", TraceID: "t-123",
				SpanTree:     "  SERVER ████        12345µs  /api/v1/inventory\n",
				SpanTreeNote: "",
			},
			want: []string{`scope-view--ok`, `200`, `req-abc`, `t-123`, `SERVER`, `/api/v1/inventory`},
		},
		{
			name: "warn",
			view: ScopeView{
				Card: "Rate limit", Method: "POST", URL: "http://localhost/auth/token",
				Status: 429, StatusText: "Too Many Requests", StatusKind: "warn",
				ResponseBody: `{"error":"rate_limited"}`,
				SpanTreeNote: "trace not yet flushed to jaeger",
			},
			want: []string{`scope-view--warn`, `429`, `rate_limited`, `trace not yet flushed`},
		},
		{
			name: "locked",
			view: lockedScope(capabilityCards()[0]),
			want: []string{`scope-view--warn`, `locked`, `sign in via the Authenticate card first`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "scope.gohtml", tc.view); err != nil {
				t.Fatalf("execute scope: %v", err)
			}
			got := buf.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("scope %s missing %q\n%s", tc.name, w, got)
				}
			}
			checkGolden(t, "scope_"+tc.name+".golden.html", buf.Bytes())
		})
	}
}

func TestMount_RoutesAndAuthCookie(t *testing.T) {
	issuer := &fakeIssuer{token: "tok-xyz", ttl: 900}
	h := Mount("", issuer)

	t.Run("index renders 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / = %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "operator console") {
			t.Errorf("expected page title in body; got %s", body[:min(200, len(body))])
		}
	})

	t.Run("static asset served", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/static/ui.css", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /static/ui.css = %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "scope-view") {
			t.Errorf("ui.css missing expected class")
		}
	})

	t.Run("auth ok sets HttpOnly cookie", func(t *testing.T) {
		form := strings.NewReader("username=admin&password=admin")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/auth/token", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /auth/token = %d, want 200", rec.Code)
		}
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatalf("expected ui_token cookie to be set")
		}
		c := cookies[0]
		if c.Name != "ui_token" || c.Value != "tok-xyz" {
			t.Errorf("unexpected cookie %+v", c)
		}
		if !c.HttpOnly {
			t.Errorf("ui_token cookie must be HttpOnly")
		}
		if c.Path != "/ui" {
			t.Errorf("ui_token cookie path = %q, want /ui", c.Path)
		}
	})

	t.Run("auth bad creds returns 401", func(t *testing.T) {
		issuer.err = errBadCreds
		defer func() { issuer.err = nil }()
		form := strings.NewReader("username=bad&password=bad")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/auth/token", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("POST /auth/token with bad creds = %d, want 401", rec.Code)
		}
	})

	t.Run("locked card returns scope view with warn kind", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/cards/rest-create/try", nil)
		h.ServeHTTP(rec, req)
		body := rec.Body.String()
		if !strings.Contains(body, "scope-view--warn") {
			t.Errorf("locked card missing warn kind:\n%s", body)
		}
		if !strings.Contains(body, "locked") {
			t.Errorf("locked card missing locked status:\n%s", body)
		}
	})

	t.Run("unknown card returns 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/cards/ghost/try", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("unknown card = %d, want 404", rec.Code)
		}
	})
}

type fakeIssuer struct {
	token string
	ttl   int64
	err   error
}

func (f *fakeIssuer) IssueFromBasic(_, _ string) (string, int64, error) {
	if f.err != nil {
		return "", 0, f.err
	}
	return f.token, f.ttl, nil
}

var errBadCreds = &badCredsError{"bad creds"}

type badCredsError struct{ s string }

func (e *badCredsError) Error() string { return e.s }

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("golden file %s missing; regenerate with `go test ./internal/web -update`", path)
		}
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s — regenerate with `go test ./internal/web -update`\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
