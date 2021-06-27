package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core/user"
)

const DefaultPageLimit = 50

func Paginate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limitStr := r.URL.Query().Get("limit")
		offsetStr := r.URL.Query().Get("offset")

		var err error
		limit := DefaultPageLimit
		if limitStr != "" {
			limit, err = strconv.Atoi(limitStr)
			if err != nil {
				limit = DefaultPageLimit
			}
		}

		offset := 0
		if offsetStr != "" {
			offset, err = strconv.Atoi(offsetStr)
			if err != nil {
				offset = 0
			}
		}

		log.Debug().Int("limit", limit).Int("offset", offset).Send()
		ctx := context.WithValue(r.Context(), "limit", limit)
		ctx = context.WithValue(ctx, "offset", offset)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type UserAccess interface {
	Login(ctx context.Context, username, password string) (user.User, error)
}

func Authenticate(ua UserAccess) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username, password, ok := r.BasicAuth()

			if !ok {
				authErr(w)
				return
			}

			u, err := ua.Login(r.Context(), username, password)
			if err != nil {
				authErr(w)
				return
			}

			ctx := context.WithValue(r.Context(), "user", u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usr, ok := r.Context().Value("user").(user.User)

		if !ok || !usr.IsAdmin {
			authErr(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func authErr(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
