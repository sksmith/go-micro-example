package httpx_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/internal/platform/httpx"
	"github.com/sksmith/go-micro-example/internal/platform/ratelimit"
)

// stubLimiter is an Allower that returns canned Decisions. The
// per-call counter lets tests assert how often the middleware
// consulted the limiter; the err field exercises the degrade-open
// branch.
type stubLimiter struct {
	calls    int32
	decision ratelimit.Decision
	err      error
}

func (s *stubLimiter) Allow(_ context.Context, _, _ string) (ratelimit.Decision, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.decision, s.err
}

func runRequest(t *testing.T, h http.Handler, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/token", strings.NewReader(""))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func okHandler(called *int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(called, 1)
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestMiddlewareAllowsRequestUnderLimit(t *testing.T) {
	limiter := &stubLimiter{decision: ratelimit.Decision{Allowed: true, Remaining: 9}}
	var handlerCalls int32
	h := httpx.Middleware(limiter, httpx.IPKey)(okHandler(&handlerCalls))

	rec := runRequest(t, h, "10.0.0.1:1234")

	if rec.Code != http.StatusNoContent {
		t.Errorf("status=%d, want 204", rec.Code)
	}
	if got := rec.Header().Get("X-RateLimit-Remaining"); got != "9" {
		t.Errorf("X-RateLimit-Remaining=%q, want 9", got)
	}
	if atomic.LoadInt32(&handlerCalls) != 1 {
		t.Errorf("handler ran %d times; want 1", handlerCalls)
	}
}

func TestMiddlewareDeniesOverLimitWith429(t *testing.T) {
	limiter := &stubLimiter{decision: ratelimit.Decision{
		Allowed:    false,
		Remaining:  0,
		RetryAfter: 7 * time.Second,
	}}
	var handlerCalls int32
	h := httpx.Middleware(limiter, httpx.IPKey)(okHandler(&handlerCalls))

	rec := runRequest(t, h, "10.0.0.1:1234")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "7" {
		t.Errorf("Retry-After=%q, want 7", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type=%q, want application/problem+json", got)
	}
	if atomic.LoadInt32(&handlerCalls) != 0 {
		t.Errorf("handler ran %d times on denial; want 0", handlerCalls)
	}
}

func TestMiddlewareDegradesOpenOnLimiterError(t *testing.T) {
	limiter := &stubLimiter{err: errors.New("redis down")}
	var handlerCalls int32
	h := httpx.Middleware(limiter, httpx.IPKey)(okHandler(&handlerCalls))

	rec := runRequest(t, h, "10.0.0.1:1234")

	if rec.Code != http.StatusNoContent {
		t.Errorf("status=%d, want 204 (degrade open)", rec.Code)
	}
	if atomic.LoadInt32(&handlerCalls) != 1 {
		t.Errorf("handler ran %d times; want 1 (degrade open should forward)", handlerCalls)
	}
}

func TestMiddlewareNilLimiterIsPassthrough(t *testing.T) {
	var handlerCalls int32
	h := httpx.Middleware(nil, httpx.IPKey)(okHandler(&handlerCalls))

	rec := runRequest(t, h, "10.0.0.1:1234")
	if rec.Code != http.StatusNoContent {
		t.Errorf("status=%d, want 204", rec.Code)
	}
	if atomic.LoadInt32(&handlerCalls) != 1 {
		t.Errorf("handler ran %d times; want 1", handlerCalls)
	}
}

func TestMiddlewareEmptyKeySkipsLimiter(t *testing.T) {
	limiter := &stubLimiter{decision: ratelimit.Decision{Allowed: false}}
	var handlerCalls int32
	noKey := func(_ *http.Request) (string, string) { return "", "ip" }
	h := httpx.Middleware(limiter, noKey)(okHandler(&handlerCalls))

	rec := runRequest(t, h, "10.0.0.1:1234")
	if rec.Code != http.StatusNoContent {
		t.Errorf("status=%d, want 204 (empty key bypasses limiter)", rec.Code)
	}
	if atomic.LoadInt32(&limiter.calls) != 0 {
		t.Errorf("limiter consulted %d times; want 0", limiter.calls)
	}
}

func TestIPKeyHonoursXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")

	key, scope := httpx.IPKey(req)
	if key != "rl:ip:203.0.113.5" {
		t.Errorf("key=%q, want rl:ip:203.0.113.5", key)
	}
	if scope != "ip" {
		t.Errorf("scope=%q, want ip", scope)
	}
}

func TestIPKeyFallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	key, _ := httpx.IPKey(req)
	if key != "rl:ip:10.0.0.1" {
		t.Errorf("key=%q, want rl:ip:10.0.0.1", key)
	}
}

// TestMiddlewareRetryAfterClampsToOneSecondMinimum confirms a deny
// with a sub-second RetryAfter still surfaces a non-zero Retry-After
// header — otherwise clients see "Retry-After: 0" and bash on the
// endpoint immediately.
func TestMiddlewareRetryAfterClampsToOneSecondMinimum(t *testing.T) {
	limiter := &stubLimiter{decision: ratelimit.Decision{
		Allowed:    false,
		RetryAfter: 100 * time.Millisecond,
	}}
	h := httpx.Middleware(limiter, httpx.IPKey)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := runRequest(t, h, "10.0.0.1:1234")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After=%q, want 1 (clamped from 100ms)", got)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"status":429`) {
		t.Errorf("problem body missing status:429: %s", string(body))
	}
}
