package user

import (
	"context"
	"crypto/rand"
	"encoding/base64"

	"errors"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core"
	"golang.org/x/crypto/bcrypt"
)

const (
	// AdminUsername is the username of the bootstrap admin account. It is
	// created on first startup if missing.
	AdminUsername = "admin"

	// SeedAdminHash is the bcrypt of the legacy "admin" password that the
	// 000002 migration used to insert. Any DB still carrying this exact
	// hash is treated as if no admin exists, so the bootstrap path runs.
	SeedAdminHash = "$2a$10$v6K6OZgz.oUPSPGQfiarAOzD6JTz2.e5hdKCkq31NglPnAsT6j1GO"

	profileProd = "prod"
)

// ErrAdminPasswordRequired is returned when running in a profile that
// refuses to auto-generate a password and no BOOTSTRAP_ADMIN_PASSWORD
// has been supplied.
var ErrAdminPasswordRequired = errors.New("admin user is missing and BOOTSTRAP_ADMIN_PASSWORD was not supplied; refusing to start")

// Bootstrap ensures an admin user exists. It implements the SEC-003
// flow:
//
//   - If the admin user is missing (or still carries the legacy seed
//     hash), create or replace it with bootstrapPassword.
//   - If bootstrapPassword is empty and profile is "prod", return
//     ErrAdminPasswordRequired.
//   - If bootstrapPassword is empty and profile is non-prod, generate
//     a random password and emit it to the log exactly once with a
//     loud warning so the operator can capture it.
//
// It is safe to call on every startup. It is a no-op when an admin
// already exists with a non-seed password.
func Bootstrap(ctx context.Context, repo Repository, profile, bootstrapPassword string) error {
	existing, getErr := repo.Get(ctx, AdminUsername)
	hasSeedAdmin := getErr == nil && existing.HashedPassword == SeedAdminHash
	switch {
	case errors.Is(getErr, core.ErrNotFound):
		// fall through; create below
	case getErr != nil:
		return getErr
	case hasSeedAdmin:
		log.Warn().Str("username", AdminUsername).Msg("seed admin password detected; replacing")
	default:
		log.Debug().Str("username", AdminUsername).Msg("admin user already present, skipping bootstrap")
		return nil
	}

	password, generated, err := resolveBootstrapPassword(profile, bootstrapPassword)
	if err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	if hasSeedAdmin {
		if err := repo.Delete(ctx, AdminUsername); err != nil {
			return err
		}
	}

	u := &User{
		Username:       AdminUsername,
		HashedPassword: string(hash),
		IsAdmin:        true,
	}
	if err := repo.Create(ctx, u); err != nil {
		return err
	}

	if generated {
		log.Warn().
			Str("username", AdminUsername).
			Str("password", password).
			Msg("bootstrap admin user created with auto-generated password; capture this now — it will not be shown again")
	} else {
		log.Info().Str("username", AdminUsername).Msg("bootstrap admin user created from BOOTSTRAP_ADMIN_PASSWORD")
	}
	return nil
}

func resolveBootstrapPassword(profile, supplied string) (password string, generated bool, err error) {
	if supplied != "" {
		return supplied, false, nil
	}
	if profile == profileProd {
		return "", false, ErrAdminPasswordRequired
	}
	gen, err := generatePassword()
	if err != nil {
		return "", false, err
	}
	return gen, true, nil
}

func generatePassword() (string, error) {
	const bytes = 24
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
