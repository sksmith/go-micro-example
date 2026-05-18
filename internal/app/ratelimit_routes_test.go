package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/app"
	"github.com/sksmith/go-micro-example/internal/auth"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
	"github.com/sksmith/go-micro-example/internal/platform/ratelimit"
)

// stubLimiter is a deterministic Allower for routing tests. The
// allowed flag and counter let tests assert that the limiter was
// consulted at the expected points without spinning up Redis.
type stubLimiter struct {
	allowed atomic.Bool
	calls   atomic.Int32
}

func (s *stubLimiter) Allow(_ context.Context, _, _ string) (ratelimit.Decision, error) {
	s.calls.Add(1)
	if s.allowed.Load() {
		return ratelimit.Decision{Allowed: true, Remaining: 1}, nil
	}
	return ratelimit.Decision{Allowed: false, RetryAfter: 5 * time.Second}, nil
}

// TestGlobalRateLimitAppliesToProtectedRoutesNotProbes asserts that
// the SEC-007 global limiter is wired around the API tree (and
// /auth/token) but stays off the liveness/readiness/metrics
// endpoints — those must remain reachable when an attacker has
// exhausted their global budget, otherwise k8s and Prometheus would
// both be locked out alongside the offender.
func TestGlobalRateLimitAppliesToProtectedRoutesNotProbes(t *testing.T) {
	cfg := config.LoadDefaults()
	invSvc, resSvc, usrSvc := getMocks()
	signer, err := auth.NewSigner(nil, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	limiter := &stubLimiter{}
	limiter.allowed.Store(false) // every request gets denied
	globalMw := httpx.Middleware(limiter, httpx.IPKeyScoped("rl:global:", "global"))

	r := app.ConfigureRouter(cfg, invSvc, resSvc, usrSvc, signer,
		nil, nil, nil, nil, globalMw, nil)

	ts := httptest.NewServer(r)
	defer ts.Close()

	t.Run("api route hits 429 when limiter denies", func(t *testing.T) {
		res, err := http.Get(ts.URL + app.ApiPath + app.InventoryPath)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusTooManyRequests {
			t.Errorf("api status=%d, want 429", res.StatusCode)
		}
		if got := res.Header.Get("Retry-After"); got == "" {
			t.Errorf("Retry-After header missing on 429")
		}
	})

	t.Run("probes bypass the limiter", func(t *testing.T) {
		before := limiter.calls.Load()
		for _, path := range []string{app.LivenessEndpoint, app.MetricsEndpoint} {
			res, err := http.Get(ts.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode == http.StatusTooManyRequests {
				t.Errorf("%s returned 429; probes must bypass the limiter", path)
			}
		}
		if limiter.calls.Load() != before {
			t.Errorf("limiter consulted on probe requests; want 0 extra calls")
		}
	})
}

// TestBodyLimitMiddlewareRejectsOversizePost asserts that the
// SEC-007 body cap is wired into the protected-route group and
// rejects requests whose Content-Length exceeds the configured
// ceiling before the handler runs.
func TestBodyLimitMiddlewareRejectsOversizePost(t *testing.T) {
	cfg := config.LoadDefaults()
	invSvc, resSvc, usrSvc := getMocks()
	signer, err := auth.NewSigner(nil, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	bodyMw := httpx.MaxBytes(16)

	r := app.ConfigureRouter(cfg, invSvc, resSvc, usrSvc, signer,
		nil, nil, nil, nil, nil, bodyMw)

	ts := httptest.NewServer(r)
	defer ts.Close()

	// Bigger-than-16 byte body posted to a real route under /api/v1
	// (auth will reject it, but the body cap must trigger first).
	huge := strings.Repeat("x", 64)
	req, err := http.NewRequest(http.MethodPost,
		ts.URL+app.ApiPath+app.InventoryPath,
		strings.NewReader(huge))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status=%d, want 413 (body cap fires before auth)", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type=%q, want application/problem+json", ct)
	}
}

// TestBodyLimitDoesNotApplyToProbes guards against accidentally
// putting the body cap on liveness/metrics — a Prometheus scrape
// won't send a body but a future probe with a Content-Length must
// not be 413'd.
func TestBodyLimitDoesNotApplyToProbes(t *testing.T) {
	cfg := config.LoadDefaults()
	invSvc, resSvc, usrSvc := getMocks()
	signer, err := auth.NewSigner(nil, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	bodyMw := httpx.MaxBytes(1) // 1 byte: would reject any non-trivial body

	r := app.ConfigureRouter(cfg, invSvc, resSvc, usrSvc, signer,
		nil, nil, nil, nil, nil, bodyMw)

	ts := httptest.NewServer(r)
	defer ts.Close()

	for _, path := range []string{app.LivenessEndpoint, app.MetricsEndpoint} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusRequestEntityTooLarge {
			t.Errorf("%s returned 413; probes must bypass the body cap", path)
		}
	}
}
