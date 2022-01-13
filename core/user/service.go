package user

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/sksmith/go-micro-example/core"
	"golang.org/x/crypto/bcrypt"
)

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

type Service interface {
	Create(ctx context.Context, user CreateUserRequest) (User, error)
	Get(ctx context.Context, username string) (User, error)
	Delete(ctx context.Context, username string) error
	Login(ctx context.Context, username, password string) (User, error)
}

type service struct {
	repo Repository
}

func (s *service) Get(ctx context.Context, username string) (User, error) {
	return s.repo.Get(ctx, username)
}

func (s *service) Create(ctx context.Context, req CreateUserRequest) (User, error) {
	if !usernameIsValid(req.Username) {
		return User{}, errors.New("invalid username")
	}
	if !passwordIsValid(req.PlainTextPassword) {
		return User{}, errors.New("invalid password")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.PlainTextPassword), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	user := &User{
		Username:       req.Username,
		HashedPassword: string(hash),
		Created:        time.Now(),
	}
	err = s.repo.Create(ctx, user)
	if err != nil {
		return User{}, err
	}
	return *user, nil
}

func usernameIsValid(username string) bool {
	return true
}

func passwordIsValid(password string) bool {
	return true
}

func (s *service) Delete(ctx context.Context, username string) error {
	return s.repo.Delete(ctx, username)
}

func (s *service) Login(ctx context.Context, username, password string) (User, error) {
	u, err := s.repo.Get(ctx, username)
	if err != nil {
		return User{}, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(u.HashedPassword), []byte(password))
	if err != nil {
		return User{}, err
	}

	return u, nil
}

type Repository interface {
	Create(ctx context.Context, user *User, tx ...core.UpdateOptions) error
	Get(ctx context.Context, username string, tx ...core.QueryOptions) (User, error)
	Delete(ctx context.Context, username string, tx ...core.UpdateOptions) error
}
