package api

import (
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/render"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/core/user"
)

func ConfigureRouter(cfg *config.Config, service inventory.Service, userService user.Service) chi.Router {
	r := chi.NewRouter()

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
	r.With(Authenticate(userService)).Route("/api/v1", func(r chi.Router) {
		r.Route("/inventory", NewInventoryApi(service).ConfigureRouter)
		r.Route("/user", NewUserApi(userService).ConfigureRouter)
	})

	return r
}
