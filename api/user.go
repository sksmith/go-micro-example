package api

import (
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core/user"
)

type UserApi struct {
	service user.Service
}

func NewUserApi(service user.Service) *UserApi {
	return &UserApi{service: service}
}

func (a *UserApi) ConfigureRouter(r chi.Router) {
	r.Route("/", func(r chi.Router) {
		r.With(AdminOnly).Post("/", a.Create)
	})
}

func (a *UserApi) Create(w http.ResponseWriter, r *http.Request) {
	data := &CreateUserRequestDto{}
	if err := render.Bind(r, data); err != nil {
		Render(w, r, ErrInvalidRequest(err))
		return
	}

	_, err := a.service.Create(r.Context(), *data.CreateUserRequest)

	if err != nil {
		log.Err(err).Send()
		Render(w, r, ErrInternalServer)
		return
	}
}
