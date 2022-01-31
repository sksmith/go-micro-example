package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/user"
)

func TestUserCreate(t *testing.T) {
	ts, mockSvc := setupUserTestServer()
	defer ts.Close()

	tests := []struct {
		name           string
		loginFunc      func(ctx context.Context, username, password string) (user.User, error)
		createFunc     func(ctx context.Context, user user.CreateUserRequest) (user.User, error)
		url            string
		request        interface{}
		wantResponse   interface{}
		wantStatusCode int
	}{
		{
			name: "admin users can create valid user",
			loginFunc: func(ctx context.Context, username, password string) (user.User, error) {
				return createUser("someadmin", "", true), nil
			},
			createFunc: func(ctx context.Context, usr user.CreateUserRequest) (user.User, error) {
				return createUser(usr.Username, "somepasswordhash", usr.IsAdmin), nil
			},
			url:            ts.URL,
			request:        createUserReq("someuser", "somepass", false),
			wantResponse:   nil,
			wantStatusCode: http.StatusOK,
		},
		{
			name: "non-admin users are unable to create users",
			loginFunc: func(ctx context.Context, username, password string) (user.User, error) {
				return createUser("someadmin", "", false), nil
			},
			createFunc: func(ctx context.Context, usr user.CreateUserRequest) (user.User, error) {
				return createUser(usr.Username, "somepasswordhash", usr.IsAdmin), nil
			},
			url:            ts.URL,
			request:        createUserReq("someuser", "somepass", false),
			wantResponse:   nil,
			wantStatusCode: http.StatusUnauthorized,
		},
		{
			name: "when the creating user is not found, server returns unauthorized",
			loginFunc: func(ctx context.Context, username, password string) (user.User, error) {
				return user.User{}, core.ErrNotFound
			},
			createFunc: func(ctx context.Context, usr user.CreateUserRequest) (user.User, error) {
				return createUser(usr.Username, "somepasswordhash", usr.IsAdmin), nil
			},
			url:            ts.URL,
			request:        createUserReq("someuser", "somepass", false),
			wantResponse:   nil,
			wantStatusCode: http.StatusUnauthorized,
		},
		{
			name: "when an unexpected error occurs logging in, an internal server error is returned",
			loginFunc: func(ctx context.Context, username, password string) (user.User, error) {
				return user.User{}, errors.New("some unexpected error")
			},
			createFunc:     nil,
			url:            ts.URL,
			request:        createUserReq("someuser", "somepass", false),
			wantResponse:   api.ErrInternalServer,
			wantStatusCode: http.StatusInternalServerError,
		},
		{
			name: "when an error occurs creating the user, an internal server error is returned",
			loginFunc: func(ctx context.Context, username, password string) (user.User, error) {
				return createUser("someadmin", "", true), nil
			},
			createFunc: func(ctx context.Context, usr user.CreateUserRequest) (user.User, error) {
				return user.User{}, errors.New("some unexpected error")
			},
			url:            ts.URL,
			request:        createUserReq("someuser", "somepass", false),
			wantResponse:   api.ErrInternalServer,
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockSvc.LoginFunc = test.loginFunc
			mockSvc.CreateFunc = test.createFunc

			res := post(test.url, test.request, t, requestOptions{username: "someuser", password: "somepass"})

			if res.StatusCode != test.wantStatusCode {
				t.Errorf("status code got=%d want=%d", res.StatusCode, test.wantStatusCode)
			}

			if test.wantStatusCode == http.StatusBadRequest ||
				test.wantStatusCode == http.StatusInternalServerError ||
				test.wantStatusCode == http.StatusNotFound {

				want := test.wantResponse.(*api.ErrResponse)
				got := &api.ErrResponse{}
				unmarshal(res, got, t)

				if got.StatusText != want.StatusText {
					t.Errorf("status text got=%s want=%s", got.StatusText, want.StatusText)
				}
				if got.ErrorText != want.ErrorText {
					t.Errorf("error text got=%s want=%s", got.ErrorText, want.ErrorText)
				}
			}
		})
	}
}

func createUser(username, password string, isAdmin bool) user.User {
	return user.User{Username: username, HashedPassword: password, IsAdmin: isAdmin}
}

func createUserReq(username, password string, isAdmin bool) api.CreateUserRequestDto {
	return api.CreateUserRequestDto{CreateUserRequest: &user.CreateUserRequest{Username: username, IsAdmin: isAdmin}, Password: password}
}

func setupUserTestServer() (*httptest.Server, *user.MockUserService) {
	svc := user.NewMockUserService()
	usrApi := api.NewUserApi(&svc)
	r := chi.NewRouter()
	r.With(api.Authenticate(&svc)).Route("/", func(r chi.Router) {
		usrApi.ConfigureRouter(r)
	})
	ts := httptest.NewServer(r)

	return ts, &svc
}
