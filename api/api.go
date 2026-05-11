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
	"github.com/sksmith/go-micro-example/core/auth"
)

const (
	LivenessEndpoint  = "/live"
	ReadinessEndpoint = "/ready"
	MetricsEndpoint   = "/metrics"

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
func ConfigureRouter(cfg *config.Config, invSvc InventoryService, resSvc ReservationService, userService UserService, signer *auth.Signer, readinessDeps map[string]Pinger) chi.Router {
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
	r.Use(CorrelationLogger)
	r.Use(Metrics)
	r.Use(render.SetContentType(render.ContentTypeJSON))
	r.Use(Logging)

	r.Handle(LivenessEndpoint, LivenessHandler())
	r.Handle(ReadinessEndpoint, ReadinessHandler(readinessDeps))
	r.Handle(MetricsEndpoint, promhttp.Handler())

	if signer != nil {
		r.Route(AuthPath, NewAuthApi(userService, signer).ConfigureRouter)
	}

	r.With(Authenticate(signer)).Route(ApiPath, func(r chi.Router) {
		r.Route(InventoryPath, NewInventoryApi(invSvc).ConfigureRouter)
		r.Route(ReservationPath, NewReservationApi(resSvc).ConfigureRouter)
		r.Route(UserPath, NewUserApi(userService).ConfigureRouter)
		r.With(AdminOnly).Route(AdminPath, func(r chi.Router) {
			r.Route(EnvPath, NewEnvApi(cfg).ConfigureRouter)
		})
	})

	return r
}

func Render(w http.ResponseWriter, r *http.Request, rnd render.Renderer) {
	if p, ok := rnd.(*Problem); ok {
		p.WriteTo(w, r)
		return
	}
	if err := render.Render(w, r, rnd); err != nil {
		log.Ctx(r.Context()).Warn().Err(err).Msg("failed to render")
	}
}

func RenderList(w http.ResponseWriter, r *http.Request, l []render.Renderer) {
	if err := render.RenderList(w, r, l); err != nil {
		log.Ctx(r.Context()).Warn().Err(err).Msg("failed to render")
	}
}
