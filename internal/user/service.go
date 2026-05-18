package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/platform/persistence"
	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidCredentials is returned by Login when the username does
// not exist or the supplied password does not match. The two cases
// are merged into a single sentinel deliberately, both to give the
// API layer a clean 401 mapping and to avoid leaking
// "user exists but password is wrong" timing/error divergence.
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrInvalidInput is the sentinel for validation failures from this
// package's service methods. The API layer maps anything wrapping
// this sentinel to HTTP 400 via errors.Is.
var ErrInvalidInput = errors.New("invalid input")

func NewService(repo Repository) *service {
	log.Info().Msg("creating user service...")

	return &service{repo: repo}
}

type service struct {
	repo Repository
}

func (s *service) Get(ctx context.Context, username string) (User, error) {
	return s.repo.Get(ctx, username)
}

func (s *service) Create(ctx context.Context, req CreateUserRequest) (User, error) {
	if !usernameIsValid(req.Username) {
		return User{}, fmt.Errorf("invalid username: %w", ErrInvalidInput)
	}
	if !passwordIsValid(req.PlainTextPassword) {
		return User{}, fmt.Errorf("invalid password: %w", ErrInvalidInput)
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
		if errors.Is(err, persistence.ErrNotFound) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.HashedPassword), []byte(password)); err != nil {
		// Don't leak the bcrypt error class to callers — both
		// ErrMismatchedHashAndPassword (wrong password) and
		// ErrHashTooShort (corrupt stored hash) collapse here.
		// A corrupt hash is a real server-side problem worth
		// logging at the boundary, but it must not produce a
		// different HTTP response than a wrong password.
		if !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			log.Ctx(ctx).Error().Err(err).Str("username", username).Msg("bcrypt comparison failed for non-mismatch reason")
		}
		return User{}, ErrInvalidCredentials
	}

	return u, nil
}

type Repository interface {
	Create(ctx context.Context, user *User, tx ...persistence.UpdateOptions) error
	Get(ctx context.Context, username string, tx ...persistence.QueryOptions) (User, error)
	Delete(ctx context.Context, username string, tx ...persistence.UpdateOptions) error
}
