package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core/user"
)

type UserService interface {
	Create(ctx context.Context, user user.CreateUserRequest) (user.User, error)
	Get(ctx context.Context, username string) (user.User, error)
	Delete(ctx context.Context, username string) error
	Login(ctx context.Context, username, password string) (user.User, error)
}

type UserApi struct {
	service UserService
}

func NewUserApi(service UserService) *UserApi {
	return &UserApi{service: service}
}

func (a *UserApi) ConfigureRouter(r chi.Router) {
	r.With(AdminOnly).Post("/", a.Create)
}

// Create registers a new user (admin only).
//
//	@Summary	Create a user
//	@Tags		user
//	@Accept		json
//	@Produce	json
//	@Param		user	body	CreateUserRequestDto	true	"new user"
//	@Success	200		"created"
//	@Failure	400		{object}	Problem
//	@Failure	401		{object}	Problem
//	@Failure	500		{object}	Problem
//	@Router		/api/v1/user [post]
//	@Security	BearerAuth
func (a *UserApi) Create(w http.ResponseWriter, r *http.Request) {
	data := &CreateUserRequestDto{}
	if err := render.Bind(r, data); err != nil {
		log.Ctx(r.Context()).Error().Err(err).Msg("failed to bind create-user request")
		Render(w, r, BadRequestProblem(err))
		return
	}

	_, err := a.service.Create(r.Context(), *data.CreateUserRequest)

	if err != nil {
		if errors.Is(err, user.ErrInvalidInput) {
			Render(w, r, BadRequestProblem(err))
			return
		}
		log.Ctx(r.Context()).Error().Err(err).Str("username", data.Username).Msg("failed to create user")
		Render(w, r, InternalServerProblem(err))
		return
	}
}
