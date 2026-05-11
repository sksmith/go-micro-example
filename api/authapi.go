package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/auth"
)

// AuthApi exposes the /auth/token exchange. Callers POST HTTP Basic
// credentials and receive a short-lived bearer JWT — the only accepted
// credential on protected routes after SEC-002c.
type AuthApi struct {
	users  UserService
	signer *auth.Signer
}

// TokenResponse mirrors the OAuth2 token-endpoint shape from RFC 6749 §5.1.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// Render satisfies render.Renderer so the response can flow through
// the project's existing render helper.
func (TokenResponse) Render(_ http.ResponseWriter, _ *http.Request) error { return nil }

func NewAuthApi(users UserService, signer *auth.Signer) *AuthApi {
	return &AuthApi{users: users, signer: signer}
}

func (a *AuthApi) ConfigureRouter(r chi.Router) {
	r.Post("/token", a.Token)
}

// Token exchanges HTTP Basic credentials for a short-lived bearer JWT.
//
//	@Summary	Issue a bearer token
//	@Tags		auth
//	@Produce	json
//	@Success	200	{object}	TokenResponse
//	@Failure	401	{object}	Problem
//	@Failure	500	{object}	Problem
//	@Router		/auth/token [post]
//	@description	Requires HTTP Basic credentials (RFC 6749 §2.3.1 OAuth2 client_credentials flow).
func (a *AuthApi) Token(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok {
		basicAuthErr(w)
		return
	}

	u, err := a.users.Login(r.Context(), username, password)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			basicAuthErr(w)
			return
		}
		log.Ctx(r.Context()).Error().Err(err).Str("username", username).Msg("error acquiring user during token issuance")
		Render(w, r, InternalServerProblem(err))
		return
	}

	signed, expiresAt, err := a.signer.Issue(u)
	if err != nil {
		log.Ctx(r.Context()).Error().Err(err).Str("username", username).Msg("error signing token")
		Render(w, r, InternalServerProblem(err))
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	render.Status(r, http.StatusOK)
	Render(w, r, TokenResponse{
		AccessToken: signed,
		TokenType:   "Bearer",
		ExpiresIn:   int64(time.Until(expiresAt).Seconds()),
	})
}
