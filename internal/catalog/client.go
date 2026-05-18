// Package catalog is the outbound-REST companion to DSN-018: a typed
// HTTP client that wraps an upstream "catalog" service with the
// timeouts, bounded retries, OpenTelemetry instrumentation, and
// request_id propagation a production caller needs.
//
// The package shape is deliberately small. The Client interface has
// one method (Lookup) so callers can mock at the seam without pulling
// in a mocking framework, and HTTPClient is a single concrete
// implementation that an outage in catalog cannot turn into an outage
// in the calling service: timeouts are bounded, retries are bounded,
// the request_id from observability.RequestIDFromContext rides along
// in X-Request-Id, and otelhttp.NewTransport wraps the underlying
// RoundTripper so each call is a child span of the inbound request.
//
// Callers are expected to degrade gracefully — Lookup is best-effort
// enrichment, not a hard dependency. The inventory API treats any
// error from Lookup as "no catalog data available" and serves the
// unenriched response.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core/observability"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ErrNotFound is the sentinel for a 404 from the upstream catalog
// service. Callers map it to "no enrichment" rather than treating it
// as a transport failure.
var ErrNotFound = errors.New("catalog: product not found")

// Product is the subset of catalog data the inventory API surfaces.
// Adding fields here means the upstream contract grew — keep this
// type and the stub catalog server in lockstep.
type Product struct {
	Sku         string `json:"sku"`
	Description string `json:"description"`
	Category    string `json:"category,omitempty"`
}

// Client is the seam tests mock at. Production code receives an
// *HTTPClient; tests pass a fake that returns canned products.
type Client interface {
	Lookup(ctx context.Context, sku string) (Product, error)
}

// Config holds the knobs that need to be tunable at startup. Defaults
// favour short timeouts and a small retry budget — an upstream that
// can't answer in a few seconds is one we should not be holding a
// request open for.
type Config struct {
	BaseURL string

	// Total deadline for one Lookup call including retries. Defaults
	// to 3s. Set to zero to use the default.
	Timeout time.Duration

	// Per-attempt timeout. The HTTP client's Timeout field is set to
	// this so a hung upstream attempt doesn't consume the full
	// retry budget. Defaults to 1s.
	PerAttemptTimeout time.Duration

	// MaxAttempts is the total number of attempts including the
	// first one. Defaults to 3 (one initial + two retries).
	MaxAttempts int

	// BackoffBase is the initial backoff between attempts. The wait
	// is base * 2^(attempt-1) with up to 50% jitter. Defaults to
	// 100ms.
	BackoffBase time.Duration

	// Transport is exposed for tests that want to stub the round
	// tripper. Production code leaves this nil; the constructor
	// wires up otelhttp.NewTransport(http.DefaultTransport).
	Transport http.RoundTripper
}

const (
	defaultTimeout      = 3 * time.Second
	defaultPerAttempt   = 1 * time.Second
	defaultMaxAttempts  = 3
	defaultBackoffBase  = 100 * time.Millisecond
	requestIDHeader     = "X-Request-Id"
	clientUserAgentName = "go-micro-example/catalog-client"
)

var (
	metricsOnce      sync.Once
	requestsTotal    *prometheus.CounterVec
	retriesTotal     prometheus.Counter
	failuresTotal    prometheus.Counter
	lookupDurationMs prometheus.Histogram
)

func ensureMetrics() {
	metricsOnce.Do(func() {
		requestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "catalog_client_requests_total",
			Help: "Outbound catalog client requests, labelled by terminal outcome (ok|not_found|error).",
		}, []string{"outcome"})
		retriesTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "catalog_client_retries_total",
			Help: "Retry attempts triggered by a 5xx or transport error.",
		})
		failuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "catalog_client_failures_total",
			Help: "Lookup calls that exhausted retries or hit a non-retryable error other than 404.",
		})
		lookupDurationMs = prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "catalog_client_lookup_duration_ms",
			Help:    "End-to-end Lookup duration in milliseconds, including retries.",
			Buckets: []float64{5, 25, 100, 250, 500, 1000, 2500, 5000},
		})
		prometheus.MustRegister(requestsTotal, retriesTotal, failuresTotal, lookupDurationMs)
	})
}

// HTTPClient is the production implementation of Client. It is safe
// for concurrent use — the underlying *http.Client is the synchronisation
// point, and the configured fields are read-only after construction.
type HTTPClient struct {
	baseURL     string
	httpClient  *http.Client
	maxAttempts int
	timeout     time.Duration
	backoffBase time.Duration
}

// NewHTTPClient constructs an HTTPClient. Passing an empty BaseURL
// returns an error — the caller should detect that earlier (the empty
// case is "catalog disabled") and not construct a client at all.
func NewHTTPClient(cfg Config) (*HTTPClient, error) {
	ensureMetrics()
	if cfg.BaseURL == "" {
		return nil, errors.New("catalog: BaseURL is required")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("catalog: parse BaseURL: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	perAttempt := cfg.PerAttemptTimeout
	if perAttempt <= 0 {
		perAttempt = defaultPerAttempt
	}
	attempts := cfg.MaxAttempts
	if attempts <= 0 {
		attempts = defaultMaxAttempts
	}
	backoff := cfg.BackoffBase
	if backoff <= 0 {
		backoff = defaultBackoffBase
	}

	transport := cfg.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	// otelhttp.NewTransport wraps the transport so each Do() emits a
	// client span as a child of the caller's context span (DSN-004).
	// W3C traceparent is injected on the wire automatically.
	transport = otelhttp.NewTransport(transport)

	return &HTTPClient{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   perAttempt,
		},
		maxAttempts: attempts,
		timeout:     timeout,
		backoffBase: backoff,
	}, nil
}

// Lookup fetches enrichment for sku. The contract:
//   - 200 OK with a JSON Product body → return the product, nil.
//   - 404 Not Found → return zero-value Product, ErrNotFound.
//   - 4xx other than 404 → return immediately without retrying; the
//     upstream is telling us the request is wrong, not transient.
//   - 5xx or transport error → retry up to MaxAttempts with
//     exponential backoff + jitter.
func (c *HTTPClient) Lookup(ctx context.Context, sku string) (Product, error) {
	if sku == "" {
		return Product{}, errors.New("catalog: sku is required")
	}

	// Total-deadline ctx: bounds the whole Lookup including retries.
	// Per-attempt timeout (httpClient.Timeout) bounds each Do().
	totalCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	endpoint, err := url.JoinPath(c.baseURL, "products", sku)
	if err != nil {
		return Product{}, fmt.Errorf("build url: %w", err)
	}

	start := time.Now()
	defer func() {
		lookupDurationMs.Observe(float64(time.Since(start).Milliseconds()))
	}()

	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if attempt > 1 {
			retriesTotal.Inc()
			wait := c.backoff(attempt - 1)
			select {
			case <-totalCtx.Done():
				return Product{}, terminalError(totalCtx.Err(), lastErr)
			case <-time.After(wait):
			}
		}

		product, retryable, err := c.doAttempt(totalCtx, endpoint, sku)
		if err == nil {
			requestsTotal.WithLabelValues("ok").Inc()
			return product, nil
		}
		if errors.Is(err, ErrNotFound) {
			requestsTotal.WithLabelValues("not_found").Inc()
			return Product{}, err
		}
		lastErr = err
		if !retryable {
			break
		}
		log.Ctx(ctx).Debug().Err(err).Int("attempt", attempt).Str("sku", sku).Msg("catalog lookup retryable error")
	}

	failuresTotal.Inc()
	requestsTotal.WithLabelValues("error").Inc()
	return Product{}, fmt.Errorf("catalog lookup exhausted retries: %w", lastErr)
}

// doAttempt runs a single HTTP call. The bool reports whether the
// caller should retry the error it returned (true for 5xx / transport
// faults, false for 4xx / decode failures / non-recoverable shape
// problems).
func (c *HTTPClient) doAttempt(ctx context.Context, endpoint, sku string) (Product, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Product{}, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", clientUserAgentName)
	if rid := observability.RequestIDFromContext(ctx); rid != "" {
		req.Header.Set(requestIDHeader, rid)
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		// Connect/TLS/header-deadline errors are all transient enough
		// to retry — the upstream may simply be slow or restarting.
		return Product{}, true, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	switch {
	case res.StatusCode == http.StatusOK:
		var product Product
		if err := json.NewDecoder(res.Body).Decode(&product); err != nil {
			// Body shape changed; retrying won't help.
			return Product{}, false, fmt.Errorf("decode: %w", err)
		}
		if product.Sku == "" {
			product.Sku = sku
		}
		return product, false, nil
	case res.StatusCode == http.StatusNotFound:
		return Product{}, false, ErrNotFound
	case res.StatusCode >= 500:
		// Drain a small slice of the body so the connection can be
		// reused; ignore decode errors.
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return Product{}, true, fmt.Errorf("upstream %d: %s", res.StatusCode, string(body))
	default:
		// 4xx other than 404: caller bug or auth problem. Don't retry.
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return Product{}, false, fmt.Errorf("upstream %d: %s", res.StatusCode, string(body))
	}
}

// backoff returns base * 2^(n-1) with ±50% jitter so concurrent
// callers don't synchronise their retries.
func (c *HTTPClient) backoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	wait := c.backoffBase << (n - 1)
	// Jitter is non-cryptographic by design — its only job is to keep
	// concurrent callers from synchronising their retries. math/rand
	// is the right tool here.
	jitter := time.Duration(rand.Int64N(int64(wait) / 2)) //#nosec G404 -- non-crypto jitter
	return wait/2 + jitter
}

// terminalError reports the right error when the total-deadline ctx
// expired mid-backoff. Prefer the last attempt's error if present —
// it's more diagnostic than "context deadline exceeded".
func terminalError(ctxErr, lastErr error) error {
	if lastErr != nil {
		return fmt.Errorf("catalog lookup deadline (%w; last attempt: %w)", ctxErr, lastErr)
	}
	return fmt.Errorf("catalog lookup deadline: %w", ctxErr)
}
