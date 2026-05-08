package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/user"
)

func TestTokenEndpointIssuesJWTForValidCredentials(t *testing.T) {
	r, usrSvc, _ := newTestRouterWithSigner()
	usrSvc.LoginFunc = func(ctx context.Context, username, password string) (user.User, error) {
		if username == "alice" && password == "pw" {
			return user.User{Username: "alice", IsAdmin: true}, nil
		}
		return user.User{}, core.ErrNotFound
	}
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+api.AuthPath+api.TokenPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("alice", "pw")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}

	var resp api.TokenResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.AccessToken == "" {
		t.Error("empty access_token")
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("token_type got=%s want=Bearer", resp.TokenType)
	}
	if resp.ExpiresIn <= 0 {
		t.Errorf("expires_in got=%d, want > 0", resp.ExpiresIn)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control got=%q want=no-store", got)
	}
}

func TestTokenEndpointRejectsBadCredentials(t *testing.T) {
	r, usrSvc, _ := newTestRouterWithSigner()
	usrSvc.LoginFunc = func(ctx context.Context, username, password string) (user.User, error) {
		return user.User{}, core.ErrNotFound
	}
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+api.AuthPath+api.TokenPath, nil)
	req.SetBasicAuth("alice", "wrong")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status got=%d want=401", res.StatusCode)
	}
}

func TestTokenEndpointRejectsMissingCredentials(t *testing.T) {
	r, _, _ := newTestRouterWithSigner()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Post(ts.URL+api.AuthPath+api.TokenPath, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status got=%d want=401", res.StatusCode)
	}
}

func TestProtectedRouteAcceptsBearerJWT(t *testing.T) {
	r, usrSvc, signer := newTestRouterWithSigner()
	usrSvc.LoginFunc = func(ctx context.Context, username, password string) (user.User, error) {
		return user.User{Username: "alice", IsAdmin: true}, nil
	}
	tok, _, err := signer.Issue(user.User{Username: "alice", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(r)
	defer ts.Close()

	loginCalls := 0
	usrSvc.LoginFunc = func(ctx context.Context, username, password string) (user.User, error) {
		loginCalls++
		return user.User{}, nil
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+api.ApiPath+api.InventoryPath, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		t.Errorf("expected non-401 with valid bearer, got %d", res.StatusCode)
	}
	if loginCalls != 0 {
		t.Errorf("Login should not be called on Bearer auth path, was called %d times", loginCalls)
	}
}

func TestProtectedRouteRejectsTamperedBearer(t *testing.T) {
	r, _, signer := newTestRouterWithSigner()
	tok, _, _ := signer.Issue(user.User{Username: "alice"})
	// Tamper a byte inside the signature segment (not at the
	// base64-url padding boundary, where flipping a char can decode
	// to the same signature byte).
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed token: %s", tok)
	}
	swap := byte('A')
	if parts[2][0] == 'A' {
		swap = 'B'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(swap) + parts[2][1:]
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+api.ApiPath+api.InventoryPath, nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 on tampered bearer, got %d", res.StatusCode)
	}
}

func TestProtectedRouteStillAcceptsBasicAuth(t *testing.T) {
	// SEC-002a is additive — Basic Auth must keep working until SEC-002c.
	r, usrSvc, _ := newTestRouterWithSigner()
	usrSvc.LoginFunc = func(ctx context.Context, username, password string) (user.User, error) {
		if username == "alice" && password == "pw" {
			return user.User{Username: "alice", IsAdmin: true}, nil
		}
		return user.User{}, core.ErrNotFound
	}
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+api.ApiPath+api.InventoryPath, nil)
	req.SetBasicAuth("alice", "pw")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		t.Errorf("expected basic auth to still work, got 401")
	}
}

func TestAdminRouteHonorsRolesClaim(t *testing.T) {
	r, _, signer := newTestRouterWithSigner()
	ts := httptest.NewServer(r)
	defer ts.Close()

	envURL := ts.URL + api.ApiPath + api.AdminPath + api.EnvPath

	t.Run("admin claim grants access", func(t *testing.T) {
		tok, _, _ := signer.Issue(user.User{Username: "alice", IsAdmin: true})
		req, _ := http.NewRequest(http.MethodGet, envURL, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized {
			t.Errorf("admin should reach env, got 401")
		}
	})

	t.Run("non-admin claim is rejected", func(t *testing.T) {
		tok, _, _ := signer.Issue(user.User{Username: "bob", IsAdmin: false})
		req, _ := http.NewRequest(http.MethodGet, envURL, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("non-admin should get 401, got %d", res.StatusCode)
		}
	})
}
