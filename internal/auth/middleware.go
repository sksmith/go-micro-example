package auth

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/user"
)

// CtxKey is the context-key type used by auth middleware to stash the
// authenticated user on the request context. Handlers downstream of
// Authenticate retrieve it via ctx.Value(CtxKeyUser).(user.User).
type CtxKey string

const CtxKeyUser CtxKey = "user"

// Authenticate requires a Bearer JWT (SEC-002c). HTTP Basic credentials
// are no longer accepted on protected routes; callers must exchange
// credentials at /auth/token (SEC-002a) and present the issued JWT.
//
// Bcrypt now runs only on the token-issue path.
func Authenticate(signer *Signer) func(http.Handler) http.Handler {
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

func authenticateBearer(token string, signer *Signer) (user.User, error) {
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

// AdminOnly rejects requests whose authenticated user is not an admin.
// Must mount inside Authenticate so CtxKeyUser is set.
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

var (
	metricsOnce      sync.Once
	authBasicCounter prometheus.Counter
	authJWTCounter   prometheus.Counter
)

func initMetrics() {
	authBasicCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_basic_requests_total",
		Help: "Successful HTTP Basic Auth requests. Retained post-SEC-002c for migration visibility; expected to stay flat at zero since Basic Auth is no longer accepted on protected routes.",
	})
	authJWTCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_jwt_requests_total",
		Help: "Number of requests authenticated via Bearer JWT.",
	})

	prometheus.MustRegister(authBasicCounter)
	prometheus.MustRegister(authJWTCounter)
}
