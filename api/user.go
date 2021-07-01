package api

import (
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
	"github.com/pkg/errors"
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
	r.Route("/v1", func(r chi.Router) {
		r.With(AdminOnly).Post("/", a.Create)
	})
}

type CreateUserRequestDto struct {
	*user.CreateUserRequest
	Password string `json:"password,omitempty"`
}

func (p *CreateUserRequestDto) Bind(_ *http.Request) error {
	if p.Username == "" || p.Password == "" {
		return errors.New("missing required field(s)")
	}

	p.CreateUserRequest.PlainTextPassword = p.Password

	return nil
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
