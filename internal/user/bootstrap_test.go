package user_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sksmith/go-micro-example/internal/platform/persistence"
	"github.com/sksmith/go-micro-example/internal/user"
	"golang.org/x/crypto/bcrypt"
)

func TestBootstrap(t *testing.T) {
	t.Run("creates admin from supplied password when missing", func(t *testing.T) {
		repo := user.NewMockRepo()
		repo.GetFunc = func(ctx context.Context, username string, _ ...persistence.QueryOptions) (user.User, error) {
			return user.User{}, persistence.ErrNotFound
		}
		var created *user.User
		repo.CreateFunc = func(ctx context.Context, u *user.User, _ ...persistence.UpdateOptions) error {
			created = u
			return nil
		}

		err := user.Bootstrap(context.Background(), repo, "local", "supplied-password")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if created == nil {
			t.Fatal("expected user to be created")
		}
		if created.Username != user.AdminUsername {
			t.Errorf("expected username %q, got %q", user.AdminUsername, created.Username)
		}
		if !created.IsAdmin {
			t.Error("expected IsAdmin true")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(created.HashedPassword), []byte("supplied-password")); err != nil {
			t.Errorf("hashed password did not match supplied: %v", err)
		}
	})

	t.Run("non-prod profile generates a random password when none supplied", func(t *testing.T) {
		repo := user.NewMockRepo()
		repo.GetFunc = func(ctx context.Context, username string, _ ...persistence.QueryOptions) (user.User, error) {
			return user.User{}, persistence.ErrNotFound
		}
		var created *user.User
		repo.CreateFunc = func(ctx context.Context, u *user.User, _ ...persistence.UpdateOptions) error {
			created = u
			return nil
		}

		err := user.Bootstrap(context.Background(), repo, "local", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if created == nil {
			t.Fatal("expected user to be created")
		}
		// The generated password is the bcrypt input, not the hash. We
		// can't recover it here, but we can confirm a hash was set.
		if !strings.HasPrefix(created.HashedPassword, "$2") {
			t.Errorf("expected bcrypt hash, got %q", created.HashedPassword)
		}
	})

	t.Run("prod profile fails fast when no password supplied", func(t *testing.T) {
		repo := user.NewMockRepo()
		repo.GetFunc = func(ctx context.Context, username string, _ ...persistence.QueryOptions) (user.User, error) {
			return user.User{}, persistence.ErrNotFound
		}
		createCalled := false
		repo.CreateFunc = func(ctx context.Context, u *user.User, _ ...persistence.UpdateOptions) error {
			createCalled = true
			return nil
		}

		err := user.Bootstrap(context.Background(), repo, "prod", "")
		if !errors.Is(err, user.ErrAdminPasswordRequired) {
			t.Fatalf("expected ErrAdminPasswordRequired, got %v", err)
		}
		if createCalled {
			t.Error("expected no user creation when prod bootstrap fails")
		}
	})

	t.Run("no-op when admin already exists with non-seed password", func(t *testing.T) {
		repo := user.NewMockRepo()
		repo.GetFunc = func(ctx context.Context, username string, _ ...persistence.QueryOptions) (user.User, error) {
			return user.User{Username: user.AdminUsername, HashedPassword: "$2a$10$realProductionHash", IsAdmin: true}, nil
		}
		createCalled := false
		repo.CreateFunc = func(ctx context.Context, u *user.User, _ ...persistence.UpdateOptions) error {
			createCalled = true
			return nil
		}
		deleteCalled := false
		repo.DeleteFunc = func(ctx context.Context, username string, _ ...persistence.UpdateOptions) error {
			deleteCalled = true
			return nil
		}

		err := user.Bootstrap(context.Background(), repo, "prod", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if createCalled || deleteCalled {
			t.Error("expected no Create or Delete when admin already exists with non-seed password")
		}
	})

	t.Run("replaces seed admin even in prod when password supplied", func(t *testing.T) {
		repo := user.NewMockRepo()
		repo.GetFunc = func(ctx context.Context, username string, _ ...persistence.QueryOptions) (user.User, error) {
			return user.User{Username: user.AdminUsername, HashedPassword: user.SeedAdminHash, IsAdmin: true}, nil
		}
		deleted := false
		repo.DeleteFunc = func(ctx context.Context, username string, _ ...persistence.UpdateOptions) error {
			deleted = true
			return nil
		}
		var created *user.User
		repo.CreateFunc = func(ctx context.Context, u *user.User, _ ...persistence.UpdateOptions) error {
			created = u
			return nil
		}

		err := user.Bootstrap(context.Background(), repo, "prod", "rotated-pw")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !deleted {
			t.Error("expected seed admin to be deleted before re-creation")
		}
		if created == nil || bcrypt.CompareHashAndPassword([]byte(created.HashedPassword), []byte("rotated-pw")) != nil {
			t.Errorf("expected new admin with rotated password, got %+v", created)
		}
	})

	t.Run("prod fails fast on seed admin without supplied password", func(t *testing.T) {
		repo := user.NewMockRepo()
		repo.GetFunc = func(ctx context.Context, username string, _ ...persistence.QueryOptions) (user.User, error) {
			return user.User{Username: user.AdminUsername, HashedPassword: user.SeedAdminHash, IsAdmin: true}, nil
		}

		err := user.Bootstrap(context.Background(), repo, "prod", "")
		if !errors.Is(err, user.ErrAdminPasswordRequired) {
			t.Fatalf("expected ErrAdminPasswordRequired, got %v", err)
		}
	})
}
