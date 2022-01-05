// TODO
package api

// import (
// 	"net/http"

// 	"github.com/go-chi/chi"
// 	"github.com/sksmith/note-server/config"
// )

// type EnvApi struct {
// 	cfg config.Config
// }

// func NewEnvApi(cfg config.Config) *EnvApi {
// 	return &EnvApi{cfg: cfg}
// }

// func (n *EnvApi) ConfigureRouter(r chi.Router) {
// 	r.Get("/", n.Get)
// }

// func (a *EnvApi) Get(w http.ResponseWriter, r *http.Request) {

// 	Render(w, r, NewEnvResponse(a.cfg))
// }
