package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/auth"
	"github.com/sksmith/go-micro-example/core/user"
)

const DefaultPageLimit = 50

type CtxKey string

const (
	CtxKeyLimit  CtxKey = "limit"
	CtxKeyOffset CtxKey = "offset"
	CtxKeyUser   CtxKey = "user"
)

func Paginate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limitStr := r.URL.Query().Get("limit")
		offsetStr := r.URL.Query().Get("offset")

		var err error
		limit := DefaultPageLimit
		if limitStr != "" {
			limit, err = strconv.Atoi(limitStr)
			if err != nil {
				limit = DefaultPageLimit
			}
		}

		offset := 0
		if offsetStr != "" {
			offset, err = strconv.Atoi(offsetStr)
			if err != nil {
				offset = 0
			}
		}

		ctx := context.WithValue(r.Context(), CtxKeyLimit, limit)
		ctx = context.WithValue(ctx, CtxKeyOffset, offset)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type UserAccess interface {
	Login(ctx context.Context, username, password string) (user.User, error)
}

// Authenticate accepts either an HTTP Basic credential or a Bearer JWT
// (SEC-002a). Both modes populate CtxKeyUser. SEC-002c will remove the
// Basic Auth path once usage is at zero.
//
// signer may be nil, in which case only Basic Auth is accepted.
//
// The header decides which path runs: a request that says "Bearer ..."
// is never tried as Basic, even if the JWT is malformed — so a typo'd
// JWT does not silently degrade to a 401-from-missing-Basic-creds.
func Authenticate(ua UserAccess, signer *auth.Signer) func(http.Handler) http.Handler {
	// Ensure the auth counters exist even if Metrics() never runs first
	// (defensive — in normal wiring Metrics() is registered before
	// Authenticate, but tests sometimes mount Authenticate alone).
	metricsOnce.Do(initMetrics)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			switch {
			case signer != nil && strings.HasPrefix(header, "Bearer "):
				u, err := authenticateBearer(strings.TrimPrefix(header, "Bearer "), signer)
				if err != nil {
					log.Debug().Err(err).Msg("bearer token rejected")
					authErr(w)
					return
				}
				authJWTCounter.Inc()
				ctx := context.WithValue(r.Context(), CtxKeyUser, u)
				next.ServeHTTP(w, r.WithContext(ctx))
			default:
				username, password, ok := r.BasicAuth()
				if !ok {
					authErr(w)
					return
				}
				u, err := ua.Login(r.Context(), username, password)
				if err != nil {
					if errors.Is(err, core.ErrNotFound) {
						authErr(w)
					} else {
						log.Error().Err(err).Str("username", username).Msg("error acquiring user")
						Render(w, r, ErrInternalServer)
					}
					return
				}
				authBasicCounter.Inc()
				ctx := context.WithValue(r.Context(), CtxKeyUser, u)
				next.ServeHTTP(w, r.WithContext(ctx))
			}
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
	w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

func Logging(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		defer func() {
			dur := fmt.Sprintf("%dms", time.Duration(time.Since(start).Milliseconds()))

			log.Trace().
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
		Help: "Number of requests authenticated via HTTP Basic Auth. Tracked per SEC-002b so SEC-002c can remove the Basic Auth path when this rate drops to zero.",
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
