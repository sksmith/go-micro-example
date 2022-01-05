package api

import (
	"errors"
	"net/http"

	"github.com/sksmith/go-micro-example/core/user"
)

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
