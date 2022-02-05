package user_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/db/usrrepo"
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
		mockRepo := usrrepo.NewMockRepo()
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
		wantRepoCallCnt map[string]int
		wantErr         bool
	}{
		{
			name:    "user is returned",
			request: user.CreateUserRequest{Username: "someuser", IsAdmin: false, PlainTextPassword: "plaintextpw"},

			wantRepoCallCnt: map[string]int{"Create": 1},
			wantUsername:    "someuser",
		},
	}

	for _, test := range tests {
		mockRepo := usrrepo.NewMockRepo()
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

			for f, c := range test.wantRepoCallCnt {
				mockRepo.VerifyCount(f, c, t)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name     string
		username string

		deleteFunc func(ctx context.Context, username string, tx ...core.UpdateOptions) error

		wantRepoCallCnt map[string]int
		wantErr         bool
	}{
		{
			name:            "user is deleted",
			username:        "someuser",
			wantRepoCallCnt: map[string]int{"Delete": 1},
		},
		{
			name:     "error is returned",
			username: "someuser",

			deleteFunc: func(ctx context.Context, username string, tx ...core.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantRepoCallCnt: map[string]int{"Delete": 1},
			wantErr:         true,
		},
	}

	for _, test := range tests {
		mockRepo := usrrepo.NewMockRepo()
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

			for f, c := range test.wantRepoCallCnt {
				mockRepo.VerifyCount(f, c, t)
			}
		})
	}
}

func TestLogin(t *testing.T) {
	usr := user.User{Username: "someuser", HashedPassword: "$2a$10$t67eB.bOkZGovKD8wqqppO7q.SqWwTS8FUrUx3GAW57GMhkD2Zcwy", IsAdmin: false, Created: time.Now()}
	tests := []struct {
		name     string
		username string
		password string

		getFunc func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error)

		wantUsername string
		wantErr      bool
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
			name:     "wrong password",
			username: "someuser",
			password: "wrongpw",

			getFunc: func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
				return usr, nil
			},

			wantErr:      true,
			wantUsername: "",
		},
		{
			name:     "unexpected error getting user",
			username: "someuser",
			password: "wrongpw",

			getFunc: func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
				return user.User{}, errors.New("some unexpected error")
			},

			wantErr:      true,
			wantUsername: "",
		},
	}

	for _, test := range tests {
		mockRepo := usrrepo.NewMockRepo()
		if test.getFunc != nil {
			mockRepo.GetFunc = test.getFunc
		}

		service := user.NewService(mockRepo)

		t.Run(test.name, func(t *testing.T) {
			got, err := service.Login(context.Background(), test.username, test.password)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if got.Username != test.wantUsername {
				t.Errorf("unexpected username got=%v want=%v", got.Username, test.wantUsername)
			}
		})
	}
}
