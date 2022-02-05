package usrrepo

import (
	"context"

	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/test"
)

type MockRepo struct {
	CreateFunc func(ctx context.Context, user *user.User, options ...core.UpdateOptions) error
	GetFunc    func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error)
	UpdateFunc func(ctx context.Context, user *user.User, options ...core.UpdateOptions) error
	DeleteFunc func(ctx context.Context, username string, options ...core.UpdateOptions) error
	*test.CallWatcher
}

func NewMockRepo() *MockRepo {
	return &MockRepo{
		CreateFunc: func(ctx context.Context, user *user.User, options ...core.UpdateOptions) error { return nil },
		GetFunc: func(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
			return user.User{}, nil
		},
		UpdateFunc:  func(ctx context.Context, user *user.User, options ...core.UpdateOptions) error { return nil },
		DeleteFunc:  func(ctx context.Context, username string, options ...core.UpdateOptions) error { return nil },
		CallWatcher: test.NewCallWatcher(),
	}
}

func (r *MockRepo) Create(ctx context.Context, user *user.User, options ...core.UpdateOptions) error {
	r.AddCall(ctx, user, options)
	return r.CreateFunc(ctx, user, options...)
}

func (r *MockRepo) Get(ctx context.Context, username string, options ...core.QueryOptions) (user.User, error) {
	r.AddCall(ctx, username, options)
	return r.GetFunc(ctx, username, options...)
}

func (r *MockRepo) Update(ctx context.Context, user *user.User, options ...core.UpdateOptions) error {
	r.AddCall(ctx, user, options)
	return r.UpdateFunc(ctx, user, options...)
}

func (r *MockRepo) Delete(ctx context.Context, username string, options ...core.UpdateOptions) error {
	r.AddCall(ctx, username, options)
	return r.DeleteFunc(ctx, username, options...)
}
