package httpx

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/platform/ratelimit"
)

// Allower is the slice of Limiter's surface the middleware actually
// uses. Exposed so tests can drive the middleware with a deterministic
// fake without reaching for a Redis (or even a Lua interpreter).
// *Limiter satisfies this interface in production code.
type Allower interface {
	Allow(ctx context.Context, key, scope string) (ratelimit.Decision, error)
}

// KeyFunc derives the bucket key + scope label from a request.
// Returning ("", _) skips the limiter entirely (useful for routes
// where we can't extract an identity yet — let the request through).
type KeyFunc func(*http.Request) (key, scope string)

// IPKey buckets by the client IP under the default "rl:ip:" namespace
// with scope label "ip". X-Forwarded-For is honoured when present
// (first hop wins) because the demo runs behind compose's proxying;
// production deployments behind a real load balancer should configure
// the LB to write a trusted header before this middleware runs, OR
// replace this with a stricter source.
func IPKey(r *http.Request) (string, string) {
	return IPKeyScoped("rl:ip:", "ip")(r)
}

// IPKeyScoped returns a KeyFunc that buckets by client IP under the
// supplied Redis-key prefix and tags metrics with scope. Use this when
// multiple Limiter instances must not share buckets — e.g. a strict
// /auth/token throttle alongside a looser global throttle. The two
// instances need distinct prefixes so their token counts are tracked
// independently in Redis and distinct scope labels so the
// ratelimit_allowed_total / ratelimit_denied_total counters separate
// them.
func IPKeyScoped(prefix, scope string) KeyFunc {
	return func(r *http.Request) (string, string) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i > 0 {
				return prefix + strings.TrimSpace(xff[:i]), scope
			}
			return prefix + strings.TrimSpace(xff), scope
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		return prefix + host, scope
	}
}

// Middleware returns an http middleware that consumes one token per
// request from the bucket keyed by keyFn(r). Denials are returned as
// 429 application/problem+json with Retry-After populated. A Redis
// error degrades open: the request is logged and forwarded to the
// next handler. The alternative — failing closed — turns a Redis
// outage into a service outage and is worse than missing throttling
// for the duration of the blip.
func Middleware(limiter Allower, keyFn KeyFunc) func(http.Handler) http.Handler {
	if limiter == nil {
		// nil limiter = pass-through. cmd/main uses this when the
		// shared Redis client wasn't wired (redis.url empty); the
		// router stays unchanged.
		return func(next http.Handler) http.Handler { return next }
	}
	if keyFn == nil {
		keyFn = IPKey
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, scope := keyFn(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			dec, err := limiter.Allow(r.Context(), key, scope)
			if err != nil {
				log.Ctx(r.Context()).Warn().Err(err).Str("key", key).Msg("rate limiter degraded open after error")
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(dec.Remaining, 10))
			if !dec.Allowed {
				retrySec := int(dec.RetryAfter / time.Second)
				if retrySec < 1 {
					retrySec = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retrySec))
				writeProblem(w, r, http.StatusTooManyRequests, "rate limit exceeded; retry after "+strconv.Itoa(retrySec)+"s")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	body, _ := json.Marshal(map[string]any{
		"type":     "about:blank",
		"title":    http.StatusText(status),
		"status":   status,
		"detail":   detail,
		"instance": r.URL.Path,
	})
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
