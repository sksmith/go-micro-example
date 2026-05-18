// The api package packages handles configuring routing for http and websocket requests into the
// server. It validates those requests and sends those to the core through the provided ports.
package api

import (
	"net/http"

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
func ConfigureRouter(cfg *config.Config, invSvc inventory.InventoryService, resSvc inventory.ReservationService, userService user.UserService, signer *auth.Signer, readinessDeps map[string]Pinger, catalogClient catalog.Client, idempotencyMw func(http.Handler) http.Handler, authRateLimitMw func(http.Handler) http.Handler) chi.Router {
	log.Info().Msg("configuring router...")
	r := chi.NewRouter()

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*.seanksmith.me", "http://*.seanksmith.me", "http://localhost:*", "https://localhost:*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	}))
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
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

	r.Handle(LivenessEndpoint, LivenessHandler())
	r.Handle(ReadinessEndpoint, ReadinessHandler(readinessDeps))
	r.Handle(MetricsEndpoint, promhttp.Handler())

	if cfg.Docs.Enabled.Value {
		r.Handle(OpenAPIEndpoint, OpenAPIHandler())
		r.Mount(DocsEndpoint, SwaggerUIHandler(cfg.AppName.Value))
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

	return r
}
