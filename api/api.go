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
	"github.com/sksmith/go-micro-example/core/user"
)

func ConfigureRouter(cfg *config.Config, invSvc InventoryService, resSvc ReservationService, userService user.Service) chi.Router {
	r := chi.NewRouter()

	r.Use(cors.Handler(cors.Options{
		// AllowedOrigins:   []string{"https://foo.com"}, // Use this to allow specific origin hosts
		AllowedOrigins: []string{"https://*.seanksmith.me", "http://*.seanksmith.me", "http://localhost*", "https://localhost*"},
		// AllowOriginFunc:  func(r *http.Request, origin string) bool { return true },
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
	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/inventory", NewInventoryApi(invSvc).ConfigureRouter)
		r.Route("/reservation", NewReservationApi(resSvc).ConfigureRouter)
		r.Route("/user", NewUserApi(userService).ConfigureRouter)
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
