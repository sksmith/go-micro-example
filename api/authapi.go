package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/auth"
)

// AuthApi exposes the /auth/token exchange (SEC-002a). Callers POST
// HTTP Basic credentials and receive a short-lived bearer JWT.
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

func (a *AuthApi) Token(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok {
		authErr(w)
		return
	}

	u, err := a.users.Login(r.Context(), username, password)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			authErr(w)
			return
		}
		log.Error().Err(err).Str("username", username).Msg("error acquiring user during token issuance")
		Render(w, r, ErrInternalServer)
		return
	}

	signed, expiresAt, err := a.signer.Issue(u)
	if err != nil {
		log.Error().Err(err).Str("username", username).Msg("error signing token")
		Render(w, r, ErrInternalServer)
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
