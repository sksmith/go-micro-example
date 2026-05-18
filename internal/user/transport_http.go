package user

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
)

type UserService interface {
	Create(ctx context.Context, user CreateUserRequest) (User, error)
	Get(ctx context.Context, username string) (User, error)
	Delete(ctx context.Context, username string) error
	Login(ctx context.Context, username, password string) (User, error)
}

type UserApi struct {
	service UserService
}

func NewUserApi(service UserService) *UserApi {
	return &UserApi{service: service}
}

// ConfigureRouter wires the user endpoints. Callers must mount this
// route behind AdminOnly — all user-management endpoints are admin-only.
func (a *UserApi) ConfigureRouter(r chi.Router) {
	r.Post("/", a.Create)
}

// Create registers a new user (admin only).
//
//	@Summary	Create a user
//	@Tags		user
//	@Accept		json
//	@Produce	json
//	@Param		user	body	CreateUserRequestDto	true	"new user"
//	@Success	200		"created"
//	@Failure	400		{object}	httpx.Problem
//	@Failure	401		{object}	httpx.Problem
//	@Failure	500		{object}	httpx.Problem
//	@Router		/api/v1/user [post]
//	@Security	BearerAuth
func (a *UserApi) Create(w http.ResponseWriter, r *http.Request) {
	data := &CreateUserRequestDto{}
	if err := render.Bind(r, data); err != nil {
		log.Ctx(r.Context()).Error().Err(err).Msg("failed to bind create-user request")
		httpx.Render(w, r, httpx.BadRequestProblem(err))
		return
	}

	_, err := a.service.Create(r.Context(), *data.CreateUserRequest)
	if err != nil {
		if errors.Is(err, ErrInvalidInput) {
			httpx.Render(w, r, httpx.BadRequestProblem(err))
			return
		}
		log.Ctx(r.Context()).Error().Err(err).Str("username", data.Username).Msg("failed to create user")
		httpx.Render(w, r, httpx.InternalServerProblem(err))
		return
	}
}
