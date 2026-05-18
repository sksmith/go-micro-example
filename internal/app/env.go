package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
)

type EnvApi struct {
	cfg *config.Config
}

func NewEnvApi(cfg *config.Config) *EnvApi {
	return &EnvApi{cfg: cfg}
}

func (n *EnvApi) ConfigureRouter(r chi.Router) {
	r.Get("/", n.Get)
}

// Get returns a redacted view of runtime configuration (admin only).
//
//	@Summary	Get redacted runtime config
//	@Tags		admin
//	@Produce	json
//	@Success	200	{object}	EnvResponse
//	@Failure	401	{object}	httpx.Problem
//	@Router		/api/v1/admin/env [get]
//	@Security	BearerAuth
func (a *EnvApi) Get(w http.ResponseWriter, r *http.Request) {
	httpx.Render(w, r, NewEnvResponse(*a.cfg))
}
