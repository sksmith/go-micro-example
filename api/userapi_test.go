package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/core/auth"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/testutil"
)

func TestUserCreate(t *testing.T) {
	ts, mockSvc, signer := setupUserTestServer(t)
	defer ts.Close()

	adminTok := issueToken(t, signer, "someadmin", true)
	nonAdminTok := issueToken(t, signer, "someuser", false)

	tests := []struct {
		name           string
		token          string
		createFunc     func(ctx context.Context, user user.CreateUserRequest) (user.User, error)
		url            string
		request        interface{}
		wantResponse   interface{}
		wantStatusCode int
	}{
		{
			name:  "admin users can create valid user",
			token: adminTok,
			createFunc: func(ctx context.Context, usr user.CreateUserRequest) (user.User, error) {
				return createUser(usr.Username, "somepasswordhash", usr.IsAdmin), nil
			},
			url:            ts.URL,
			request:        createUserReq("someuser", "somepass", false),
			wantResponse:   nil,
			wantStatusCode: http.StatusOK,
		},
		{
			name:  "non-admin users are unable to create users",
			token: nonAdminTok,
			createFunc: func(ctx context.Context, usr user.CreateUserRequest) (user.User, error) {
				return createUser(usr.Username, "somepasswordhash", usr.IsAdmin), nil
			},
			url:            ts.URL,
			request:        createUserReq("someuser", "somepass", false),
			wantResponse:   nil,
			wantStatusCode: http.StatusUnauthorized,
		},
		{
			name:           "requests without a bearer token are rejected",
			token:          "",
			createFunc:     nil,
			url:            ts.URL,
			request:        createUserReq("someuser", "somepass", false),
			wantResponse:   nil,
			wantStatusCode: http.StatusUnauthorized,
		},
		{
			name:           "requests with a tampered bearer token are rejected",
			token:          adminTok + "tamper",
			createFunc:     nil,
			url:            ts.URL,
			request:        createUserReq("someuser", "somepass", false),
			wantResponse:   nil,
			wantStatusCode: http.StatusUnauthorized,
		},
		{
			name:  "when an error occurs creating the user, an internal server error is returned",
			token: adminTok,
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
			mockSvc.CreateFunc = test.createFunc

			res := testutil.Post(test.url, test.request, t, testutil.RequestOptions{Token: test.token})

			if res.StatusCode != test.wantStatusCode {
				t.Errorf("status code got=%d want=%d", res.StatusCode, test.wantStatusCode)
			}

			if test.wantStatusCode == http.StatusBadRequest ||
				test.wantStatusCode == http.StatusInternalServerError ||
				test.wantStatusCode == http.StatusNotFound {

				want := test.wantResponse.(*api.ErrResponse)
				got := &api.ErrResponse{}
				testutil.Unmarshal(res, got, t)

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

func setupUserTestServer(t *testing.T) (*httptest.Server, *user.MockUserService, *auth.Signer) {
	t.Helper()
	svc := user.NewMockUserService()
	usrApi := api.NewUserApi(svc)
	signer, err := auth.NewSigner(nil, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.With(api.Authenticate(signer)).Route("/", func(r chi.Router) {
		usrApi.ConfigureRouter(r)
	})
	ts := httptest.NewServer(r)

	return ts, svc, signer
}

func issueToken(t *testing.T, signer *auth.Signer, username string, isAdmin bool) string {
	t.Helper()
	tok, _, err := signer.Issue(user.User{Username: username, IsAdmin: isAdmin})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
