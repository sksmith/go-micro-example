package api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/api"
)

type fakePinger struct {
	err   error
	delay time.Duration
}

func (f fakePinger) Ping(ctx context.Context) error {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func TestLivenessHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	api.LivenessHandler()(rec, httptest.NewRequest(http.MethodGet, "/live", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status got=%d want=200", rec.Code)
	}
	if got := rec.Body.String(); got != "OK" {
		t.Errorf("body got=%q want=%q", got, "OK")
	}
}

func TestReadinessHandlerHealthy(t *testing.T) {
	rec := httptest.NewRecorder()
	api.ReadinessHandler(map[string]api.Pinger{
		"db": fakePinger{},
	})(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status got=%d want=200", rec.Code)
	}
}

func TestReadinessHandlerNoDeps(t *testing.T) {
	// nil map should be safe (an empty readiness configuration
	// is "always ready" — useful for tests and bootstrap).
	rec := httptest.NewRecorder()
	api.ReadinessHandler(nil)(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status got=%d want=200", rec.Code)
	}
}

func TestReadinessHandlerFailingDep(t *testing.T) {
	rec := httptest.NewRecorder()
	api.ReadinessHandler(map[string]api.Pinger{
		"db": fakePinger{err: errors.New("connection refused")},
	})(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status got=%d want=503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "db:") {
		t.Errorf("body should name the failing dep, got %q", body)
	}
	if !strings.Contains(body, "connection refused") {
		t.Errorf("body should include the error reason, got %q", body)
	}
}

func TestReadinessHandlerSlowDepTimesOut(t *testing.T) {
	// readinessTimeout is 1s; a 2s delay must trigger the
	// deadline path and surface "timeout" in the body.
	rec := httptest.NewRecorder()
	start := time.Now()
	api.ReadinessHandler(map[string]api.Pinger{
		"db": fakePinger{delay: 2 * time.Second},
	})(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	elapsed := time.Since(start)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status got=%d want=503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "timeout") {
		t.Errorf("body should classify timeout failures, got %q", rec.Body.String())
	}
	// The probe must return well inside the slow ping's duration —
	// otherwise the probe itself would stall. Allow CI slack but
	// the upper bound has to be < the 2s delay.
	if elapsed >= 2*time.Second {
		t.Errorf("readiness probe blocked for %s, exceeded its own timeout", elapsed)
	}
}

func TestReadinessHandlerMultipleDepsAllReported(t *testing.T) {
	rec := httptest.NewRecorder()
	api.ReadinessHandler(map[string]api.Pinger{
		"db":   fakePinger{err: errors.New("db down")},
		"amqp": fakePinger{err: errors.New("amqp down")},
	})(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status got=%d want=503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "db:") || !strings.Contains(body, "amqp:") {
		t.Errorf("body should list every failing dep, got %q", body)
	}
}

// TestHealthEndpointsMounted is a small belt-and-braces test that
// the router actually exposes /live and /ready. Catches a
// regression where a refactor stops wiring one of them.
func TestHealthEndpointsMounted(t *testing.T) {
	r, _, _ := newTestRouterWithSigner()
	ts := httptest.NewServer(r)
	defer ts.Close()

	for _, path := range []string{api.LivenessEndpoint, api.ReadinessEndpoint} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status got=%d want=200", path, resp.StatusCode)
		}
	}
}
