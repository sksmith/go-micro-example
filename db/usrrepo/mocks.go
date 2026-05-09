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

	CreateCalls int
	GetCalls    int
	UpdateCalls int
	DeleteCalls int
}

func NewMockRepo() *MockRepo {
	return &MockRepo{
		CreateFunc: func(ctx context.Context, user *user.User, options ...core.UpdateOptions) error { return nil },
		GetFunc: func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
			return user.User{}, nil
		},
		UpdateFunc: func(ctx context.Context, user *user.User, options ...core.UpdateOptions) error { return nil },
		DeleteFunc: func(ctx context.Context, username string, options ...core.UpdateOptions) error { return nil },
	}
}

func (r *MockRepo) Create(ctx context.Context, user *user.User, options ...core.UpdateOptions) error {
	r.CreateCalls++
	return r.CreateFunc(ctx, user, options...)
}

func (r *MockRepo) Get(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
	r.GetCalls++
	return r.GetFunc(ctx, username, options...)
}

func (r *MockRepo) Update(ctx context.Context, user *user.User, options ...core.UpdateOptions) error {
	r.UpdateCalls++
	return r.UpdateFunc(ctx, user, options...)
}

func (r *MockRepo) Delete(ctx context.Context, username string, options ...core.UpdateOptions) error {
	r.DeleteCalls++
	return r.DeleteFunc(ctx, username, options...)
}
