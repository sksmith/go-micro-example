package user

import "time"

type CreateUserRequest struct {
	Username          string `json:"username,omitempty"`
	IsAdmin           bool   `json:"isAdmin,omitempty"`
	PlainTextPassword string `json:"-"`
}

type User struct {
	Username string
	HashedPassword string
	IsAdmin bool
	Created time.Time
}