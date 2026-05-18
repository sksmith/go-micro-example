package user

import (
	"context"

	"github.com/sksmith/go-micro-example/internal/platform/persistence"
)

type MockRepo struct {
	CreateFunc func(ctx context.Context, u *User, options ...persistence.UpdateOptions) error
	GetFunc    func(ctx context.Context, username string, options ...persistence.QueryOptions) (User, error)
	UpdateFunc func(ctx context.Context, u *User, options ...persistence.UpdateOptions) error
	DeleteFunc func(ctx context.Context, username string, options ...persistence.UpdateOptions) error

	CreateCalls int
	GetCalls    int
	UpdateCalls int
	DeleteCalls int
}

func NewMockRepo() *MockRepo {
	return &MockRepo{
		CreateFunc: func(ctx context.Context, u *User, options ...persistence.UpdateOptions) error { return nil },
		GetFunc: func(ctx context.Context, username string, options ...persistence.QueryOptions) (User, error) {
			return User{}, nil
		},
		UpdateFunc: func(ctx context.Context, u *User, options ...persistence.UpdateOptions) error { return nil },
		DeleteFunc: func(ctx context.Context, username string, options ...persistence.UpdateOptions) error { return nil },
	}
}

func (r *MockRepo) Create(ctx context.Context, u *User, options ...persistence.UpdateOptions) error {
	r.CreateCalls++
	return r.CreateFunc(ctx, u, options...)
}

func (r *MockRepo) Get(ctx context.Context, username string, options ...persistence.QueryOptions) (User, error) {
	r.GetCalls++
	return r.GetFunc(ctx, username, options...)
}

func (r *MockRepo) Update(ctx context.Context, u *User, options ...persistence.UpdateOptions) error {
	r.UpdateCalls++
	return r.UpdateFunc(ctx, u, options...)
}

func (r *MockRepo) Delete(ctx context.Context, username string, options ...persistence.UpdateOptions) error {
	r.DeleteCalls++
	return r.DeleteFunc(ctx, username, options...)
}
