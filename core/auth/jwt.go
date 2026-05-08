// Package auth provides JWT issuance and verification for the SEC-002a
// additive-JWT phase. Both Basic Auth and Bearer JWTs work in parallel;
// SEC-002c removes Basic Auth.
package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/sksmith/go-micro-example/core/user"
)

const (
	// MinSigningKeyBytes mirrors the HS256 recommendation in RFC 7518:
	// the key MUST be at least as long as the hash output (256 bits).
	MinSigningKeyBytes = 32

	defaultIssuer   = "go-micro-example"
	defaultAudience = "go-micro-example"
	defaultTTL      = 15 * time.Minute
)

// ErrSigningKeyMissing is returned by NewSigner when no key is supplied
// and the caller asked for strict (production) behaviour.
var ErrSigningKeyMissing = errors.New("JWT signing key missing or shorter than 32 bytes")

// Signer issues and validates JWTs. It is safe for concurrent use; the
// only mutable state is the once-per-process signing key.
type Signer struct {
	key       []byte
	ttl       time.Duration
	issuer    string
	audience  string
	ephemeral bool
}

// Claims is the JWT body issued by this service. It embeds
// jwt.RegisteredClaims for sub / iss / aud / iat / exp / jti.
type Claims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles"`
}

// NewSigner constructs a Signer from a raw key.
//
//   - If key is at least MinSigningKeyBytes long, it's used as-is.
//   - If key is empty/too short and strict is true (prod profile),
//     ErrSigningKeyMissing is returned. The caller should fail-fast.
//   - If key is empty/too short and strict is false (non-prod), an
//     ephemeral 32-byte key is generated. The Signer's Ephemeral()
//     reports true so the caller can log a warning. Tokens signed
//     with an ephemeral key do not survive a process restart.
//
// ttl 0 means "use the default" (15 minutes).
func NewSigner(key []byte, ttl time.Duration, strict bool) (*Signer, error) {
	if len(key) < MinSigningKeyBytes {
		if strict {
			return nil, ErrSigningKeyMissing
		}
		gen := make([]byte, MinSigningKeyBytes)
		if _, err := rand.Read(gen); err != nil {
			return nil, fmt.Errorf("generate ephemeral key: %w", err)
		}
		return &Signer{
			key:       gen,
			ttl:       resolveTTL(ttl),
			issuer:    defaultIssuer,
			audience:  defaultAudience,
			ephemeral: true,
		}, nil
	}
	return &Signer{
		key:      append([]byte(nil), key...),
		ttl:      resolveTTL(ttl),
		issuer:   defaultIssuer,
		audience: defaultAudience,
	}, nil
}

func resolveTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultTTL
	}
	return ttl
}

// Ephemeral reports whether the signing key was randomly generated at
// startup (true) or supplied by the operator (false). Issued tokens
// become invalid across restarts when this is true.
func (s *Signer) Ephemeral() bool { return s.ephemeral }

// TTL is the lifetime applied to newly issued tokens.
func (s *Signer) TTL() time.Duration { return s.ttl }

// Issue creates a signed JWT for the given user.
func (s *Signer) Issue(u user.User) (token string, expiresAt time.Time, err error) {
	now := time.Now().UTC()
	expiresAt = now.Add(s.ttl)
	roles := []string{}
	if u.IsAdmin {
		roles = append(roles, "admin")
	}
	c := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.Username,
			Issuer:    s.issuer,
			Audience:  jwt.ClaimStrings{s.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			ID:        uuid.NewString(),
		},
		Roles: roles,
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	signed, err := t.SignedString(s.key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign jwt: %w", err)
	}
	return signed, expiresAt, nil
}

// Parse verifies the signature and standard claims of a token and
// returns the parsed claims on success. It returns an error for
// missing / wrong-signature / expired / wrong-issuer / wrong-audience
// tokens.
func (s *Signer) Parse(token string) (*Claims, error) {
	c := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.key, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, err
	}
	if !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	if c.Issuer != s.issuer {
		return nil, fmt.Errorf("unexpected issuer: %s", c.Issuer)
	}
	if !c.VerifyAudience(s.audience, true) {
		return nil, errors.New("unexpected audience")
	}
	return c, nil
}
