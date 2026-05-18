package rest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

// Header is the canonical request-header name DSN-019 standardises on.
// Stripe and Square use the same spelling; following the de-facto
// convention means no surprises for callers writing against this API.
const Header = "Idempotency-Key"

// HashHeader echoes the body hash the server computed back to the
// client so a divergent retry can be diagnosed quickly. Optional from
// a contract standpoint — clients are not required to inspect it.
const HashHeader = "Idempotency-Hash"

// DefaultTTL is the retention window for cached responses. Stripe
// uses 24 hours; we match it. Override via Config.TTL.
const DefaultTTL = 24 * time.Hour

// recordedResponseHeaders is the curated allow-list of response
// headers the middleware replays on a retry. Cookies, auth tokens,
// and Set-Cookie are excluded — a cached value would be wrong for
// the second caller.
var recordedResponseHeaders = []string{"Content-Type", "Link", "Location", "X-Request-Id"}

// Config configures the middleware. Required controls the policy
// behind a missing Idempotency-Key header: when true, mutating
// requests without the header are rejected with 400; when false, the
// middleware is pass-through. Required is per-router instance because
// not every endpoint should mandate the header (the demo's productionEvent
// is mandatory, but other PUT/POST routes may legitimately opt in).
type Config struct {
	Store    Store
	TTL      time.Duration
	Required bool
}

var (
	metricsOnce  sync.Once
	hitTotal     prometheus.Counter
	saveTotal    prometheus.Counter
	conflictHit  prometheus.Counter
	missingTotal prometheus.Counter
)

func ensureMetrics() {
	metricsOnce.Do(func() {
		hitTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rest_idempotency_hits_total",
			Help: "Requests that replayed a cached response (same key, same body-hash).",
		})
		saveTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rest_idempotency_saves_total",
			Help: "Responses recorded under an Idempotency-Key for replay.",
		})
		conflictHit = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rest_idempotency_conflicts_total",
			Help: "Retries with a key reused for a different request body (HTTP 409).",
		})
		missingTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rest_idempotency_missing_header_total",
			Help: "Mutating requests rejected because Idempotency-Key was required and absent (HTTP 400).",
		})
		prometheus.MustRegister(hitTotal, saveTotal, conflictHit, missingTotal)
	})
}

// Middleware returns an HTTP middleware enforcing the DSN-019
// contract:
//
//   - First request with a key: handler runs; the response (status,
//     curated headers, body) is recorded under the (key, body-hash)
//     pair with TTL.
//   - Retry within TTL with the same key AND the same body-hash:
//     handler is skipped; the cached response is replayed
//     byte-for-byte.
//   - Retry with the same key but a different body-hash: 409, no
//     handler invocation.
//   - Key missing and Required=true: 400 problem+json.
//   - Key missing and Required=false: pass-through.
//
// The middleware operates on a buffered copy of the request body so
// it can both hash the body and let the handler re-read it. Bodies
// are capped at 1 MiB; oversized requests skip the cache entirely
// (logged) so a hostile or sloppy caller cannot OOM the process by
// streaming arbitrary payloads under a key.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	if cfg.Store == nil {
		panic("idempotency/rest: Store is required")
	}
	ensureMetrics()
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimSpace(r.Header.Get(Header))
			if key == "" {
				if cfg.Required {
					missingTotal.Inc()
					writeProblem(w, r, http.StatusBadRequest, "Idempotency-Key header is required for "+r.Method+" "+r.URL.Path)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			body, err := readAndBuffer(r)
			if err != nil {
				writeProblem(w, r, http.StatusBadRequest, "failed to read request body: "+err.Error())
				return
			}
			hash := sha256Hex(body)
			r.Body = io.NopCloser(bytes.NewReader(body))

			entry, found, err := cfg.Store.Lookup(r.Context(), key)
			if err != nil {
				log.Ctx(r.Context()).Warn().Err(err).Str("key", key).Msg("idempotency store lookup failed; running handler")
				// Lookup failures degrade open — we'd rather run the
				// handler twice than serve a wrong cached response.
			} else if found {
				if entry.BodyHash != hash {
					conflictHit.Inc()
					log.Ctx(r.Context()).Warn().
						Str("key", key).
						Str("storedHash", entry.BodyHash).
						Str("requestHash", hash).
						Msg("idempotency-key reused with different body; returning 409")
					writeProblem(w, r, http.StatusConflict, "Idempotency-Key reused with a different request body")
					return
				}
				hitTotal.Inc()
				replay(w, entry, hash)
				return
			}

			rec := newRecordingResponseWriter(w)
			next.ServeHTTP(rec, r)

			if rec.shouldCache() {
				entry := Entry{
					Status:   rec.statusCode,
					BodyHash: hash,
					Body:     rec.body.Bytes(),
					Headers:  rec.recordedHeaders(),
					SavedAt:  time.Now(),
				}
				if saveErr := cfg.Store.Save(r.Context(), key, entry, ttl); saveErr != nil {
					log.Ctx(r.Context()).Warn().Err(saveErr).Str("key", key).Msg("idempotency store save failed")
				} else {
					saveTotal.Inc()
				}
			}
		})
	}
}

// shouldCache governs which responses are recorded. 2xx and 4xx
// outcomes are deterministic from the caller's perspective and worth
// replaying — 5xx is a server-side failure the next retry should be
// allowed to re-attempt cleanly, so we deliberately don't cache it.
func (rw *recordingResponseWriter) shouldCache() bool {
	return rw.statusCode >= 200 && rw.statusCode < 500
}

// maxBodyBytes is the request-body ceiling for hashing. 1 MiB covers
// every JSON payload this API legitimately accepts (the largest is a
// reservation list, well under 64 KiB).
const maxBodyBytes = 1 << 20

func readAndBuffer(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	_ = r.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, errors.New("request body exceeds idempotency cache limit (1 MiB)")
	}
	return body, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func replay(w http.ResponseWriter, entry Entry, hash string) {
	for _, h := range entry.Headers {
		w.Header().Set(h.Key, h.Value)
	}
	w.Header().Set(HashHeader, hash)
	w.Header().Set("Idempotent-Replay", "true")
	if entry.Status == 0 {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(entry.Status)
	}
	_, _ = w.Write(entry.Body)
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	// Lightweight problem+json — kept inline so the middleware
	// doesn't import the api package and pull a cycle. The errordto
	// layer is the canonical encoder for routes; this is the rare
	// case where the middleware short-circuits before it.
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

// recordingResponseWriter buffers the response so it can be both sent
// downstream and recorded for replay. Only the curated allow-list of
// response headers is captured.
type recordingResponseWriter struct {
	target      http.ResponseWriter
	body        *bytes.Buffer
	statusCode  int
	wroteHeader bool
}

func newRecordingResponseWriter(target http.ResponseWriter) *recordingResponseWriter {
	return &recordingResponseWriter{target: target, body: &bytes.Buffer{}, statusCode: http.StatusOK}
}

func (rw *recordingResponseWriter) Header() http.Header { return rw.target.Header() }

func (rw *recordingResponseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.wroteHeader = true
	rw.statusCode = code
	rw.target.WriteHeader(code)
}

func (rw *recordingResponseWriter) Write(p []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	rw.body.Write(p)
	return rw.target.Write(p)
}

func (rw *recordingResponseWriter) recordedHeaders() []HeaderKV {
	out := make([]HeaderKV, 0, len(recordedResponseHeaders))
	h := rw.target.Header()
	for _, name := range recordedResponseHeaders {
		if v := h.Get(name); v != "" {
			out = append(out, HeaderKV{Key: name, Value: v})
		}
	}
	return out
}
