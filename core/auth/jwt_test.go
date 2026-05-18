package auth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/sksmith/go-micro-example/core/auth"
	"github.com/sksmith/go-micro-example/internal/user"
)

const validKey = "0123456789abcdef0123456789abcdef" // 32 bytes

func TestNewSigner(t *testing.T) {
	t.Run("strict mode without key returns error", func(t *testing.T) {
		_, err := auth.NewSigner(nil, 0, true)
		if !errors.Is(err, auth.ErrSigningKeyMissing) {
			t.Fatalf("expected ErrSigningKeyMissing, got %v", err)
		}
	})

	t.Run("strict mode with short key returns error", func(t *testing.T) {
		_, err := auth.NewSigner([]byte("short"), 0, true)
		if !errors.Is(err, auth.ErrSigningKeyMissing) {
			t.Fatalf("expected ErrSigningKeyMissing, got %v", err)
		}
	})

	t.Run("non-strict mode without key generates ephemeral", func(t *testing.T) {
		s, err := auth.NewSigner(nil, 0, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !s.Ephemeral() {
			t.Error("expected ephemeral signer")
		}
	})

	t.Run("supplied key is not ephemeral", func(t *testing.T) {
		s, err := auth.NewSigner([]byte(validKey), 0, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Ephemeral() {
			t.Error("expected non-ephemeral signer")
		}
	})

	t.Run("custom ttl honored", func(t *testing.T) {
		s, _ := auth.NewSigner([]byte(validKey), 5*time.Minute, true)
		if s.TTL() != 5*time.Minute {
			t.Errorf("ttl got=%v want=5m", s.TTL())
		}
	})
}

func TestRoundTrip(t *testing.T) {
	s, _ := auth.NewSigner([]byte(validKey), 0, true)
	tok, exp, err := s.Issue(user.User{Username: "alice", IsAdmin: true})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if time.Until(exp) <= 0 {
		t.Error("expiry should be in the future")
	}

	claims, err := s.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != "alice" {
		t.Errorf("subject got=%s want=alice", claims.Subject)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "admin" {
		t.Errorf("roles got=%v want=[admin]", claims.Roles)
	}
}

func TestNonAdminHasEmptyRoles(t *testing.T) {
	s, _ := auth.NewSigner([]byte(validKey), 0, true)
	tok, _, _ := s.Issue(user.User{Username: "bob", IsAdmin: false})
	claims, err := s.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(claims.Roles) != 0 {
		t.Errorf("roles got=%v want=[]", claims.Roles)
	}
}

func TestParseRejectsExpired(t *testing.T) {
	// 1ns TTL guarantees expiry by the time we parse.
	s, _ := auth.NewSigner([]byte(validKey), time.Nanosecond, true)
	tok, _, _ := s.Issue(user.User{Username: "alice"})
	time.Sleep(2 * time.Millisecond)
	if _, err := s.Parse(tok); err == nil {
		t.Error("expected error on expired token")
	}
}

func TestParseRejectsTampered(t *testing.T) {
	s, _ := auth.NewSigner([]byte(validKey), 0, true)
	tok, _, _ := s.Issue(user.User{Username: "alice"})
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed token: %s", tok)
	}
	// Tamper the *first* signature byte — base64-url's last char often
	// only encodes padding bits, so flipping it can decode to the same
	// signature (test was flaky). The first char always carries real
	// bits.
	sig := parts[2]
	first := sig[0]
	swap := byte('A')
	if first == 'A' {
		swap = 'B'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(swap) + sig[1:]
	if _, err := s.Parse(tampered); err == nil {
		t.Error("expected error on tampered signature")
	}
}

func TestParseRejectsWrongKey(t *testing.T) {
	signer, _ := auth.NewSigner([]byte(validKey), 0, true)
	other, _ := auth.NewSigner([]byte("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"), 0, true)
	tok, _, _ := signer.Issue(user.User{Username: "alice"})
	if _, err := other.Parse(tok); err == nil {
		t.Error("expected error parsing token signed with a different key")
	}
}

func TestParseRejectsAlgNone(t *testing.T) {
	// A token signed with the "none" algorithm is the classic JWT
	// downgrade attack. Confirm we reject it.
	s, _ := auth.NewSigner([]byte(validKey), 0, true)
	noneToken := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{Subject: "alice"})
	signed, err := noneToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := s.Parse(signed); err == nil {
		t.Error("expected error on alg=none token")
	}
}
