package user_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/internal/user"
)

func TestGet(t *testing.T) {
	usr := user.User{Username: "someuser", HashedPassword: "somehashedpassword", IsAdmin: false, Created: time.Now()}
	tests := []struct {
		name     string
		username string

		getFunc func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error)

		wantUser user.User
		wantErr  bool
	}{
		{
			name:     "user is returned",
			username: "someuser",

			getFunc: func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
				return usr, nil
			},

			wantUser: usr,
		},
		{
			name:     "error is returned",
			username: "someuser",

			getFunc: func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
				return user.User{}, errors.New("some unexpected error")
			},

			wantErr:  true,
			wantUser: user.User{},
		},
	}

	for _, test := range tests {
		mockRepo := user.NewMockRepo()
		if test.getFunc != nil {
			mockRepo.GetFunc = test.getFunc
		}

		service := user.NewService(mockRepo)

		t.Run(test.name, func(t *testing.T) {
			got, err := service.Get(context.Background(), test.username)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if !reflect.DeepEqual(got, test.wantUser) {
				t.Errorf("unexpected user\n got=%+v\nwant=%+v", got, test.wantUser)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name    string
		request user.CreateUserRequest

		createFunc func(ctx context.Context, user *user.User, tx ...core.UpdateOptions) error

		wantUsername    string
		wantCreateCalls int
		wantErr         bool
	}{
		{
			name:    "user is returned",
			request: user.CreateUserRequest{Username: "someuser", IsAdmin: false, PlainTextPassword: "plaintextpw"},

			wantCreateCalls: 1,
			wantUsername:    "someuser",
		},
	}

	for _, test := range tests {
		mockRepo := user.NewMockRepo()
		if test.createFunc != nil {
			mockRepo.CreateFunc = test.createFunc
		}

		service := user.NewService(mockRepo)

		t.Run(test.name, func(t *testing.T) {
			got, err := service.Create(context.Background(), test.request)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if got.Username != test.wantUsername {
				t.Errorf("unexpected username got=%+v want=%+v", got.Username, test.wantUsername)
			}

			if mockRepo.CreateCalls != test.wantCreateCalls {
				t.Errorf("Create calls got=%d want=%d", mockRepo.CreateCalls, test.wantCreateCalls)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name     string
		username string

		deleteFunc func(ctx context.Context, username string, tx ...core.UpdateOptions) error

		wantDeleteCalls int
		wantErr         bool
	}{
		{
			name:            "user is deleted",
			username:        "someuser",
			wantDeleteCalls: 1,
		},
		{
			name:     "error is returned",
			username: "someuser",

			deleteFunc: func(ctx context.Context, username string, tx ...core.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantDeleteCalls: 1,
			wantErr:         true,
		},
	}

	for _, test := range tests {
		mockRepo := user.NewMockRepo()
		if test.deleteFunc != nil {
			mockRepo.DeleteFunc = test.deleteFunc
		}

		service := user.NewService(mockRepo)

		t.Run(test.name, func(t *testing.T) {
			err := service.Delete(context.Background(), test.username)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if mockRepo.DeleteCalls != test.wantDeleteCalls {
				t.Errorf("Delete calls got=%d want=%d", mockRepo.DeleteCalls, test.wantDeleteCalls)
			}
		})
	}
}

func TestLogin(t *testing.T) {
	usr := user.User{Username: "someuser", HashedPassword: "$2a$10$t67eB.bOkZGovKD8wqqppO7q.SqWwTS8FUrUx3GAW57GMhkD2Zcwy", IsAdmin: false, Created: time.Now()}
	unexpected := errors.New("some unexpected error")

	tests := []struct {
		name     string
		username string
		password string

		getFunc func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error)

		wantUsername string
		// wantErr is the sentinel the caller should see. nil means
		// no error. ERR-001 B1: both "user not found" and "wrong
		// password" must collapse to ErrInvalidCredentials so the
		// API layer can render 401 without leaking which one
		// happened.
		wantErr error
	}{
		{
			name:     "correct password",
			username: "someuser",
			password: "plaintextpw",

			getFunc: func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
				return usr, nil
			},

			wantUsername: "someuser",
		},
		{
			name:     "wrong password collapses to ErrInvalidCredentials",
			username: "someuser",
			password: "wrongpw",

			getFunc: func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
				return usr, nil
			},

			wantErr: user.ErrInvalidCredentials,
		},
		{
			name:     "user not found collapses to ErrInvalidCredentials",
			username: "missing",
			password: "anything",

			getFunc: func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
				return user.User{}, core.ErrNotFound
			},

			wantErr: user.ErrInvalidCredentials,
		},
		{
			name:     "unexpected repo error propagates as-is (not collapsed)",
			username: "someuser",
			password: "wrongpw",

			getFunc: func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
				return user.User{}, unexpected
			},

			wantErr: unexpected,
		},
	}

	for _, test := range tests {
		mockRepo := user.NewMockRepo()
		if test.getFunc != nil {
			mockRepo.GetFunc = test.getFunc
		}

		service := user.NewService(mockRepo)

		t.Run(test.name, func(t *testing.T) {
			got, err := service.Login(context.Background(), test.username, test.password)
			switch {
			case test.wantErr == nil && err != nil:
				t.Errorf("did not want error, got=%v", err)
			case test.wantErr != nil && !errors.Is(err, test.wantErr):
				t.Errorf("expected errors.Is(err, %v), got %v", test.wantErr, err)
			}

			if got.Username != test.wantUsername {
				t.Errorf("unexpected username got=%v want=%v", got.Username, test.wantUsername)
			}
		})
	}
}
