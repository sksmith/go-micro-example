package usrrepo

import (
	"context"

	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/user"
)

type MockRepo struct {
	CreateFunc func(ctx context.Context, user *user.User, tx ...core.Transaction) error
	GetFunc    func(ctx context.Context, username string, tx ...core.Transaction) (user.User, error)
	UpdateFunc func(ctx context.Context, user *user.User, tx ...core.Transaction) error
	DeleteFunc func(ctx context.Context, username string, tx ...core.Transaction) error
}

func (r MockRepo) Create(ctx context.Context, user *user.User, tx ...core.Transaction) error {
	return r.CreateFunc(ctx, user, tx...)
}

func (r MockRepo) Get(ctx context.Context, username string, tx ...core.Transaction) (user.User, error) {
	return r.GetFunc(ctx, username, tx...)
}

func (r MockRepo) Update(ctx context.Context, user *user.User, tx ...core.Transaction) error {
	return r.UpdateFunc(ctx, user, tx...)
}

func (r MockRepo) Delete(ctx context.Context, username string, tx ...core.Transaction) error {
	return r.DeleteFunc(ctx, username, tx...)
}
