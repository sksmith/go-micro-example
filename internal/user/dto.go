package user

import (
	"errors"
	"net/http"
)

type CreateUserRequestDto struct {
	*CreateUserRequest
	Password string `json:"password,omitempty"`
}

func (p *CreateUserRequestDto) Bind(_ *http.Request) error {
	if p.Username == "" || p.Password == "" {
		return errors.New("missing required field(s)")
	}

	p.PlainTextPassword = p.Password

	return nil
}
