// The api package packages handles configuring routing for http and websocket requests into the
// server. It validates those requests and sends those to the core through the provided ports.
package app

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/render"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/riandyrn/otelchi"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/auth"
	"github.com/sksmith/go-micro-example/internal/catalog"
	"github.com/sksmith/go-micro-example/internal/inventory"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
	"github.com/sksmith/go-micro-example/internal/user"
)

const (
	LivenessEndpoint  = "/live"
	ReadinessEndpoint = "/ready"
	MetricsEndpoint   = "/metrics"

	OpenAPIEndpoint = "/openapi.yaml"
	DocsEndpoint    = "/docs"

	ApiPath         = "/api/v1"
	InventoryPath   = "/inventory"
	ReservationPath = "/reservation"
	UserPath        = "/user"
	AdminPath       = "/admin"
	EnvPath         = "/env"
	AuthPath        = "/auth"
	TokenPath       = "/token"
)

// ConfigureRouter instantiates a go-chi router with middleware and routes for the server.
//
// readinessDeps is the map of name → Pinger that /ready checks
// on every probe. Callers pass at minimum {"db": pgPool}; pass
// nil for an empty map (legacy tests).
// catalogClient is a nil-safe alias for the outbound catalog client
// (DSN-018). Pass nil to leave inventory responses unenriched.
//
// idempotencyMw is the optional DSN-019 Idempotency-Key middleware.
// nil leaves the mutating routes unwrapped; non-nil applies it to
// productionEvent and the reservation Create route.
//
// authRateLimitMw is the optional DSN-021b rate-limit middleware
// applied to /auth/token. nil leaves the route un-throttled.
//
// globalRateLimitMw is the optional SEC-007 per-IP rate-limit
// applied to every protected route (everything except the probe and
// metrics endpoints). nil leaves the routes un-throttled.
// bodyLimitMw is the optional SEC-007 request-body cap (defaults to
// 1 MiB). nil disables the cap.
func ConfigureRouter(cfg *config.Config, invSvc inventory.InventoryService, resSvc inventory.ReservationService, userService user.UserService, signer *auth.Signer, readinessDeps map[string]Pinger, catalogClient catalog.Client, idempotencyMw func(http.Handler) http.Handler, authRateLimitMw func(http.Handler) http.Handler, globalRateLimitMw func(http.Handler) http.Handler, bodyLimitMw func(http.Handler) http.Handler) chi.Router {
	log.Info().Msg("configuring router...")
	r := chi.NewRouter()

	// SEC-006: CORS uses an explicit, environment-driven list of exact
	// origins. AllowCredentials is only safe alongside an exact list,
	// never a wildcard. An empty list disables CORS entirely: go-chi's
	// cors handler with no allowed origins emits no
	// Access-Control-Allow-Origin header, which is the right behavior
	// for a same-origin or first-party-only deployment.
	if origins := parseCORSOrigins(cfg.CORS.AllowedOrigins.Value); len(origins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins: origins,
			AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			// X-CSRF-Token removed: SEC-002c took the project to
			// bearer-token auth, and no CSRF token strategy exists
			// to back the header. Re-add only when a real one does.
			AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
			ExposedHeaders:   []string{"Link"},
			AllowCredentials: cfg.CORS.AllowCredentials.Value,
			MaxAge:           300, // Maximum value not ignored by any of major browsers
		}))
	}
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	// HSTS only emits a header on TLS requests (direct or via
	// X-Forwarded-Proto: https), so mounting it unconditionally is
	// safe for plaintext local dev too.
	r.Use(httpx.HSTS(httpx.HSTSOptions{IncludeSubDomains: true}))
	// otelchi mounts before Metrics/Logging so the span covers
	// both. The chi route pattern (e.g. /api/v1/inventory/{sku})
	// is the span name, which is what you want for grouping —
	// not the literal URL with the sku interpolated.
	r.Use(otelchi.Middleware(cfg.AppName.Value, otelchi.WithChiRoutes(r)))
	// CorrelationLogger must mount after RequestID + otelchi (so it can
	// read the request and trace IDs they set) and before everything
	// that emits logs, so log.Ctx(ctx) carries the correlation fields.
	r.Use(httpx.CorrelationLogger)
	r.Use(httpx.Metrics)
	r.Use(render.SetContentType(render.ContentTypeJSON))
	r.Use(httpx.Logging)

	// Probe and metrics endpoints stay outside the rate-limit + body
	// cap group so that Kubernetes liveness/readiness checks and
	// Prometheus scrapes never trip the throttle or get a 413 on a
	// stray Content-Length. They still pick up the upper middleware
	// chain (CORS, RequestID, tracing, logging) above.
	r.Handle(LivenessEndpoint, LivenessHandler())
	r.Handle(ReadinessEndpoint, ReadinessHandler(readinessDeps))
	r.Handle(MetricsEndpoint, promhttp.Handler())

	if cfg.Docs.Enabled.Value {
		r.Handle(OpenAPIEndpoint, OpenAPIHandler())
		r.Mount(DocsEndpoint, SwaggerUIHandler(cfg.AppName.Value))
	}

	// SEC-007: everything user-facing — auth and the API tree — sits
	// inside a Group that applies the global per-IP rate limit and
	// the request-body cap. Each is nil-safe; nil middleware is a
	// pass-through so tests and minimal compositions stay easy.
	r.Group(func(r chi.Router) {
		if globalRateLimitMw != nil {
			r.Use(globalRateLimitMw)
		}
		if bodyLimitMw != nil {
			r.Use(bodyLimitMw)
		}

		if signer != nil {
			authApi := auth.NewAuthApi(userService, signer)
			authApi.SetRateLimit(authRateLimitMw)
			r.Route(AuthPath, authApi.ConfigureRouter)
		}

		r.With(auth.Authenticate(signer)).Route(ApiPath, func(r chi.Router) {
			invApi := inventory.NewInventoryApi(invSvc)
			invApi.SetCatalog(catalogClient)
			invApi.SetIdempotency(idempotencyMw)
			r.Route(InventoryPath, invApi.ConfigureRouter)
			resApi := inventory.NewReservationApi(resSvc)
			resApi.SetIdempotency(idempotencyMw)
			r.Route(ReservationPath, resApi.ConfigureRouter)
			r.With(auth.AdminOnly).Route(UserPath, user.NewUserApi(userService).ConfigureRouter)
			r.With(auth.AdminOnly).Route(AdminPath, func(r chi.Router) {
				r.Route(EnvPath, NewEnvApi(cfg).ConfigureRouter)
			})
		})
	})

	return r
}

// parseCORSOrigins splits the comma-separated cors.allowedOrigins
// config value into a clean slice. Whitespace and empty entries are
// dropped so a trailing comma or a "  " entry in YAML doesn't quietly
// permit the empty-string origin (which go-chi's matcher would treat
// as a wildcard).
func parseCORSOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
