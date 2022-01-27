// The api package packages handles configuring routing for http and websocket requests into the
// server. It validates those requests and sends those to the core through the provided ports.
package api

import (
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/render"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/config"
)

const (
	HealthEndpoint  = "/health"
	EnvEndpoint     = "/env"
	MetricsEndpoint = "/metrics"

	ApiPath         = "/api/v1"
	InventoryPath   = "/inventory"
	ReservationPath = "/reservation"
	UserPath        = "/user"
)

// ConfigureRouter instantiates a go-chi router with middleware and routes for the server
func ConfigureRouter(cfg *config.Config, invSvc InventoryService, resSvc ReservationService, userService UserService) chi.Router {
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
	r.Use(Metrics)
	r.Use(render.SetContentType(render.ContentTypeJSON))
	r.Use(Logging)

	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("UP"))
	})
	r.Handle("/metrics", promhttp.Handler())
	r.Route("/env", NewEnvApi(cfg).ConfigureRouter)

	// TODO Enable authentication, how to handle this for websockets?
	// r.With(Authenticate(userService)).Route("/api/v1", func(r chi.Router) {

	r.Route(ApiPath, func(r chi.Router) {
		r.Route(InventoryPath, NewInventoryApi(invSvc).ConfigureRouter)
		r.Route(ReservationPath, NewReservationApi(resSvc).ConfigureRouter)
		r.Route(UserPath, NewUserApi(userService).ConfigureRouter)
	})

	return r
}

func Render(w http.ResponseWriter, r *http.Request, rnd render.Renderer) {
	if err := render.Render(w, r, rnd); err != nil {
		log.Warn().Err(err).Msg("failed to render")
	}
}

func RenderList(w http.ResponseWriter, r *http.Request, l []render.Renderer) {
	if err := render.RenderList(w, r, l); err != nil {
		log.Warn().Err(err).Msg("failed to render")
	}
}
