package httpx

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/platform/observability"
	"go.opentelemetry.io/otel/trace"
)

// CorrelationLogger installs a request-scoped zerolog logger into the
// request context so downstream code can call log.Ctx(ctx) and pick up
// request_id (and trace_id/span_id when an OTel span is recording)
// without threading those values manually. Mount AFTER chi's RequestID
// and otelchi middleware so both IDs are available.
//
// It also echoes the request and trace IDs back to the caller as
// X-Request-Id / X-Trace-Id response headers so external observers
// (the DSN-027 operator-console UI, retry middleware, support
// scripts, curl debugging) can correlate the response to the same
// span the server logged. Headers are written before the handler
// runs so they survive even when the handler returns an error and
// flushes its own status code immediately.
func CorrelationLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.GetReqID(ctx)
		ctx = observability.ContextWithRequestID(ctx, reqID)

		if reqID != "" {
			w.Header().Set("X-Request-Id", reqID)
		}

		zctx := log.With().Str("request_id", reqID)
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			zctx = zctx.
				Str("trace_id", sc.TraceID().String()).
				Str("span_id", sc.SpanID().String())
			w.Header().Set("X-Trace-Id", sc.TraceID().String())
		}
		logger := zctx.Logger()
		next.ServeHTTP(w, r.WithContext(logger.WithContext(ctx)))
	})
}

func Logging(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		defer func() {
			dur := fmt.Sprintf("%dms", time.Duration(time.Since(start).Milliseconds()))

			// SEC-010: redact the query string before logging so a
			// `?token=…` (or any other key in SensitiveQueryParams)
			// cannot leak into application logs. Path and non-sensitive
			// query keys are preserved.
			log.Ctx(r.Context()).Trace().
				Str("method", r.Method).
				Str("host", r.Host).
				Str("uri", RedactURI(r.URL)).
				Str("proto", r.Proto).
				Str("origin", r.Header.Get("Origin")).
				Int("status", ww.Status()).
				Int("bytes", ww.BytesWritten()).
				Str("duration", dur).Send()
		}()
		next.ServeHTTP(ww, r)
	}

	return http.HandlerFunc(fn)
}

var (
	urlMetricsOnce sync.Once
	urlHitCount    *prometheus.CounterVec
	urlLatency     *prometheus.SummaryVec
)

func initURLMetrics() {
	urlHitCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "url_hit_count",
			Help: "Number of times the given url was hit",
		},
		[]string{"method", "url"},
	)
	urlLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "url_latency",
			Help:       "The latency quantiles for the given URL",
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		},
		[]string{"method", "url"},
	)

	prometheus.MustRegister(urlHitCount)
	prometheus.MustRegister(urlLatency)
}

func Metrics(next http.Handler) http.Handler {
	urlMetricsOnce.Do(initURLMetrics)

	fn := func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		defer func() {
			ctx := chi.RouteContext(r.Context())

			if len(ctx.RoutePatterns) > 0 {
				dur := float64(time.Since(start).Milliseconds())
				urlLatency.WithLabelValues(ctx.RouteMethod, ctx.RoutePatterns[0]).Observe(dur)
				urlHitCount.WithLabelValues(ctx.RouteMethod, ctx.RoutePatterns[0]).Inc()
			}
		}()

		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}
