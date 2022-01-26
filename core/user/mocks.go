package user

import "context"

type MockUserService struct {
	CreateFunc func(ctx context.Context, user CreateUserRequest) (User, error)
	GetFunc    func(ctx context.Context, username string) (User, error)
	DeleteFunc func(ctx context.Context, username string) error
	LoginFunc  func(ctx context.Context, username, password string) (User, error)
}

func NewMockUserService() MockUserService {
	return MockUserService{
		CreateFunc: func(ctx context.Context, user CreateUserRequest) (User, error) { return User{}, nil },
		GetFunc:    func(ctx context.Context, username string) (User, error) { return User{}, nil },
		DeleteFunc: func(ctx context.Context, username string) error { return nil },
		LoginFunc:  func(ctx context.Context, username, password string) (User, error) { return User{}, nil },
	}
}

func (u *MockUserService) Create(ctx context.Context, user CreateUserRequest) (User, error) {
	return u.CreateFunc(ctx, user)
}

func (u *MockUserService) Get(ctx context.Context, username string) (User, error) {
	return u.GetFunc(ctx, username)
}

func (u *MockUserService) Delete(ctx context.Context, username string) error {
	return u.DeleteFunc(ctx, username)
}

func (u *MockUserService) Login(ctx context.Context, username, password string) (User, error) {
	return u.LoginFunc(ctx, username, password)
}
