package usrrepo

import (
	"context"

	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/user"
)

type MockRepo struct {
	CreateFunc func(ctx context.Context, user *user.User, options ...core.UpdateOptions) error
	GetFunc    func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error)
	UpdateFunc func(ctx context.Context, user *user.User, options ...core.UpdateOptions) error
	DeleteFunc func(ctx context.Context, username string, options ...core.UpdateOptions) error
}

func (r MockRepo) Create(ctx context.Context, user *user.User, options ...core.UpdateOptions) error {
	return r.CreateFunc(ctx, user, options...)
}

func (r MockRepo) Get(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
	return r.GetFunc(ctx, username, options...)
}

func (r MockRepo) Update(ctx context.Context, user *user.User, options ...core.UpdateOptions) error {
	return r.UpdateFunc(ctx, user, options...)
}

func (r MockRepo) Delete(ctx context.Context, username string, options ...core.UpdateOptions) error {
	return r.DeleteFunc(ctx, username, options...)
}
