package app

import (
	"context"
	"fmt"
	"time"

	"github.com/sksmith/go-micro-example/internal/auth"
	"github.com/sksmith/go-micro-example/internal/user"
)

// uiTokenIssuer adapts the existing user.UserService + auth.Signer
// pair to the web.TokenIssuer interface the DSN-027 UI needs. Keeping
// the adapter in internal/app — rather than internal/web — avoids
// pulling the inventory/user packages into the otherwise-leaf web
// package so its template-only API stays easy to test in isolation.
type uiTokenIssuer struct {
	users  user.UserService
	signer *auth.Signer
}

// IssueFromBasic mirrors the /auth/token handler: resolve the user by
// password, then sign a short-lived bearer JWT. Errors are deliberately
// flattened — the UI surfaces only "invalid credentials" so neither
// password presence nor admin status leaks via the response shape.
func (u uiTokenIssuer) IssueFromBasic(username, password string) (string, int64, error) {
	if u.signer == nil {
		return "", 0, fmt.Errorf("ui auth: signer not configured")
	}
	usr, err := u.users.Login(context.Background(), username, password)
	if err != nil {
		return "", 0, err
	}
	token, expiresAt, err := u.signer.Issue(usr)
	if err != nil {
		return "", 0, err
	}
	return token, int64(time.Until(expiresAt).Seconds()), nil
}
