package main

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
)

// runRateLimitBurst exercises DSN-021b end-to-end. The step sends a
// burst of /auth/token requests in rapid succession and asserts at
// least one 429 came back. The default config is 1 token/sec, burst
// 5; sending 20 requests in a tight loop blows past the bucket
// regardless of how much it has refilled since the previous demo
// step. Earlier steps each spent one token via fetchToken, and the
// natural gaps between steps refill the bucket — so the bucket
// state at this point is at most a few tokens, definitely not
// enough to absorb 20 in a burst.
//
// This step runs LAST in the demo so a half-empty bucket can't
// starve subsequent steps' fetchToken calls.
func runRateLimitBurst(ctx context.Context, cfg Config) (string, error) {
	const attempts = 20

	var denied int32
	for i := 0; i < attempts; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/auth/token", nil)
		if err != nil {
			return "", fmt.Errorf("build req: %w", err)
		}
		req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("request %d: %w", i, err)
		}
		_ = res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests {
			atomic.AddInt32(&denied, 1)
		}
	}

	got := atomic.LoadInt32(&denied)
	if got == 0 {
		return "", fmt.Errorf("sent %d /auth/token requests in a burst; zero 429s came back (rate limiter is not engaged)", attempts)
	}
	// No specific upper bound — just confirming the limiter fired
	// at least once. With burst=5 and 20 attempts, we'd expect
	// ~15 denials, but the exact number depends on inter-request
	// latency and any refill that happened mid-loop.
	return "", nil
}
