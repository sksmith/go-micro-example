package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core/auth"
	"github.com/sksmith/go-micro-example/core/observability"
	"github.com/sksmith/go-micro-example/internal/user"
	"go.opentelemetry.io/otel/trace"
)

// CorrelationLogger installs a request-scoped zerolog logger into the
// request context so downstream code can call log.Ctx(ctx) and pick up
// request_id (and trace_id/span_id when an OTel span is recording)
// without threading those values manually. Mount AFTER chi's RequestID
// and otelchi middleware so both IDs are available.
func CorrelationLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.GetReqID(ctx)
		ctx = observability.ContextWithRequestID(ctx, reqID)

		zctx := log.With().Str("request_id", reqID)
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			zctx = zctx.
				Str("trace_id", sc.TraceID().String()).
				Str("span_id", sc.SpanID().String())
		}
		logger := zctx.Logger()
		next.ServeHTTP(w, r.WithContext(logger.WithContext(ctx)))
	})
}

type CtxKey string

const CtxKeyUser CtxKey = "user"

// Authenticate requires a Bearer JWT (SEC-002c). HTTP Basic credentials
// are no longer accepted on protected routes; callers must exchange
// credentials at /auth/token (SEC-002a) and present the issued JWT.
//
// Bcrypt now runs only on the token-issue path.
func Authenticate(signer *auth.Signer) func(http.Handler) http.Handler {
	metricsOnce.Do(initMetrics)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if signer == nil || !strings.HasPrefix(header, "Bearer ") {
				authErr(w)
				return
			}
			u, err := authenticateBearer(strings.TrimPrefix(header, "Bearer "), signer)
			if err != nil {
				log.Ctx(r.Context()).Debug().Err(err).Msg("bearer token rejected")
				authErr(w)
				return
			}
			authJWTCounter.Inc()
			ctx := context.WithValue(r.Context(), CtxKeyUser, u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authenticateBearer(token string, signer *auth.Signer) (user.User, error) {
	claims, err := signer.Parse(token)
	if err != nil {
		return user.User{}, err
	}
	u := user.User{Username: claims.Subject}
	for _, role := range claims.Roles {
		if role == "admin" {
			u.IsAdmin = true
		}
	}
	return u, nil
}

func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usr, ok := r.Context().Value(CtxKeyUser).(user.User)

		if !ok || !usr.IsAdmin {
			authErr(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func authErr(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="restricted"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

func basicAuthErr(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

func Logging(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		defer func() {
			dur := fmt.Sprintf("%dms", time.Duration(time.Since(start).Milliseconds()))

			log.Ctx(r.Context()).Trace().
				Str("method", r.Method).
				Str("host", r.Host).
				Str("uri", r.RequestURI).
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
	metricsOnce      sync.Once
	urlHitCount      *prometheus.CounterVec
	urlLatency       *prometheus.SummaryVec
	authBasicCounter prometheus.Counter
	authJWTCounter   prometheus.Counter
)

func initMetrics() {
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

	authBasicCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_basic_requests_total",
		Help: "Successful HTTP Basic Auth requests. Retained post-SEC-002c for migration visibility; expected to stay flat at zero since Basic Auth is no longer accepted on protected routes.",
	})
	authJWTCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_jwt_requests_total",
		Help: "Number of requests authenticated via Bearer JWT.",
	})

	prometheus.MustRegister(urlHitCount)
	prometheus.MustRegister(urlLatency)
	prometheus.MustRegister(authBasicCounter)
	prometheus.MustRegister(authJWTCounter)
}

func Metrics(next http.Handler) http.Handler {
	metricsOnce.Do(initMetrics)

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
