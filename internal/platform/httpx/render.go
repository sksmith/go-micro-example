package httpx

import (
	"net/http"

	"github.com/go-chi/render"
	"github.com/rs/zerolog/log"
)

// Render writes a renderable response. Problems are routed through
// (*Problem).WriteTo so the application/problem+json content type is
// preserved; everything else goes through go-chi/render. Always route
// problems through httpx.Render — never render.Render — so the
// WriteTo path is taken.
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
