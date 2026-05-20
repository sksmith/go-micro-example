package user_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/internal/platform/persistence"
	"github.com/sksmith/go-micro-example/internal/user"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// withRecordingTracer swaps the global TracerProvider for the
// duration of one test and returns a SpanRecorder so DSN-004b's
// service-layer span assertions can read what the methods produced.
// Restores prior globals on cleanup so tests stay independent.
func withRecordingTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return recorder
}

func TestGet(t *testing.T) {
	usr := user.User{Username: "someuser", HashedPassword: "somehashedpassword", IsAdmin: false, Created: time.Now()}
	tests := []struct {
		name     string
		username string

		getFunc func(ctx context.Context, username string, options ...persistence.QueryOptions) (user.User, error)

		wantUser user.User
		wantErr  bool
	}{
		{
			name:     "user is returned",
			username: "someuser",

			getFunc: func(ctx context.Context, username string, options ...persistence.QueryOptions) (user.User, error) {
				return usr, nil
			},

			wantUser: usr,
		},
		{
			name:     "error is returned",
			username: "someuser",

			getFunc: func(ctx context.Context, username string, options ...persistence.QueryOptions) (user.User, error) {
				return user.User{}, errors.New("some unexpected error")
			},

			wantErr:  true,
			wantUser: user.User{},
		},
	}

	for _, test := range tests {
		mockRepo := user.NewMockRepo()
		if test.getFunc != nil {
			mockRepo.GetFunc = test.getFunc
		}

		service := user.NewService(mockRepo)

		t.Run(test.name, func(t *testing.T) {
			got, err := service.Get(context.Background(), test.username)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if !reflect.DeepEqual(got, test.wantUser) {
				t.Errorf("unexpected user\n got=%+v\nwant=%+v", got, test.wantUser)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name    string
		request user.CreateUserRequest

		createFunc func(ctx context.Context, user *user.User, tx ...persistence.UpdateOptions) error

		wantUsername    string
		wantCreateCalls int
		wantErr         bool
	}{
		{
			name:    "user is returned",
			request: user.CreateUserRequest{Username: "someuser", IsAdmin: false, PlainTextPassword: "plaintextpw"},

			wantCreateCalls: 1,
			wantUsername:    "someuser",
		},
	}

	for _, test := range tests {
		mockRepo := user.NewMockRepo()
		if test.createFunc != nil {
			mockRepo.CreateFunc = test.createFunc
		}

		service := user.NewService(mockRepo)

		t.Run(test.name, func(t *testing.T) {
			got, err := service.Create(context.Background(), test.request)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if got.Username != test.wantUsername {
				t.Errorf("unexpected username got=%+v want=%+v", got.Username, test.wantUsername)
			}

			if mockRepo.CreateCalls != test.wantCreateCalls {
				t.Errorf("Create calls got=%d want=%d", mockRepo.CreateCalls, test.wantCreateCalls)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name     string
		username string

		deleteFunc func(ctx context.Context, username string, tx ...persistence.UpdateOptions) error

		wantDeleteCalls int
		wantErr         bool
	}{
		{
			name:            "user is deleted",
			username:        "someuser",
			wantDeleteCalls: 1,
		},
		{
			name:     "error is returned",
			username: "someuser",

			deleteFunc: func(ctx context.Context, username string, tx ...persistence.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantDeleteCalls: 1,
			wantErr:         true,
		},
	}

	for _, test := range tests {
		mockRepo := user.NewMockRepo()
		if test.deleteFunc != nil {
			mockRepo.DeleteFunc = test.deleteFunc
		}

		service := user.NewService(mockRepo)

		t.Run(test.name, func(t *testing.T) {
			err := service.Delete(context.Background(), test.username)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if mockRepo.DeleteCalls != test.wantDeleteCalls {
				t.Errorf("Delete calls got=%d want=%d", mockRepo.DeleteCalls, test.wantDeleteCalls)
			}
		})
	}
}

func TestLogin(t *testing.T) {
	usr := user.User{Username: "someuser", HashedPassword: "$2a$10$t67eB.bOkZGovKD8wqqppO7q.SqWwTS8FUrUx3GAW57GMhkD2Zcwy", IsAdmin: false, Created: time.Now()}
	unexpected := errors.New("some unexpected error")

	tests := []struct {
		name     string
		username string
		password string

		getFunc func(ctx context.Context, username string, options ...persistence.QueryOptions) (user.User, error)

		wantUsername string
		// wantErr is the sentinel the caller should see. nil means
		// no error. ERR-001 B1: both "user not found" and "wrong
		// password" must collapse to ErrInvalidCredentials so the
		// API layer can render 401 without leaking which one
		// happened.
		wantErr error
	}{
		{
			name:     "correct password",
			username: "someuser",
			password: "plaintextpw",

			getFunc: func(ctx context.Context, username string, options ...persistence.QueryOptions) (user.User, error) {
				return usr, nil
			},

			wantUsername: "someuser",
		},
		{
			name:     "wrong password collapses to ErrInvalidCredentials",
			username: "someuser",
			password: "wrongpw",

			getFunc: func(ctx context.Context, username string, options ...persistence.QueryOptions) (user.User, error) {
				return usr, nil
			},

			wantErr: user.ErrInvalidCredentials,
		},
		{
			name:     "user not found collapses to ErrInvalidCredentials",
			username: "missing",
			password: "anything",

			getFunc: func(ctx context.Context, username string, options ...persistence.QueryOptions) (user.User, error) {
				return user.User{}, persistence.ErrNotFound
			},

			wantErr: user.ErrInvalidCredentials,
		},
		{
			name:     "unexpected repo error propagates as-is (not collapsed)",
			username: "someuser",
			password: "wrongpw",

			getFunc: func(ctx context.Context, username string, options ...persistence.QueryOptions) (user.User, error) {
				return user.User{}, unexpected
			},

			wantErr: unexpected,
		},
	}

	for _, test := range tests {
		mockRepo := user.NewMockRepo()
		if test.getFunc != nil {
			mockRepo.GetFunc = test.getFunc
		}

		service := user.NewService(mockRepo)

		t.Run(test.name, func(t *testing.T) {
			got, err := service.Login(context.Background(), test.username, test.password)
			switch {
			case test.wantErr == nil && err != nil:
				t.Errorf("did not want error, got=%v", err)
			case test.wantErr != nil && !errors.Is(err, test.wantErr):
				t.Errorf("expected errors.Is(err, %v), got %v", test.wantErr, err)
			}

			if got.Username != test.wantUsername {
				t.Errorf("unexpected username got=%v want=%v", got.Username, test.wantUsername)
			}
		})
	}
}

// TestUserService_SpansHappyAndError pins DSN-004b's contract on the
// user side: every exported service method records exactly one span
// per call, named after the method, with the username attribute set;
// methods that returned an error mark the span Error and record the
// error message via RecordError.
func TestUserService_SpansHappyAndError(t *testing.T) {
	t.Run("happy path: Get records span without error", func(t *testing.T) {
		recorder := withRecordingTracer(t)

		mockRepo := user.NewMockRepo()
		mockRepo.GetFunc = func(_ context.Context, _ string, _ ...persistence.QueryOptions) (user.User, error) {
			return user.User{Username: "alice"}, nil
		}
		svc := user.NewService(mockRepo)

		if _, err := svc.Get(context.Background(), "alice"); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}

		spans := recorder.Ended()
		if len(spans) != 1 {
			t.Fatalf("recorded %d spans, want 1", len(spans))
		}
		got := spans[0]
		if got.Name() != "Get" {
			t.Errorf("span name = %q, want %q", got.Name(), "Get")
		}
		if got.SpanKind() != trace.SpanKindInternal {
			t.Errorf("kind = %v, want internal", got.SpanKind())
		}
		if got.Status().Code.String() == "Error" {
			t.Errorf("happy path should not set status Error, got %q (%q)",
				got.Status().Code.String(), got.Status().Description)
		}
		if !hasStringAttr(got, "user.username", "alice") {
			t.Errorf("missing user.username=alice attr; got %+v", got.Attributes())
		}
	})

	t.Run("error path: Login records error on span", func(t *testing.T) {
		recorder := withRecordingTracer(t)

		mockRepo := user.NewMockRepo()
		mockRepo.GetFunc = func(_ context.Context, _ string, _ ...persistence.QueryOptions) (user.User, error) {
			return user.User{}, persistence.ErrNotFound
		}
		svc := user.NewService(mockRepo)

		_, err := svc.Login(context.Background(), "ghost", "pw")
		if !errors.Is(err, user.ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}

		spans := recorder.Ended()
		if len(spans) != 1 {
			t.Fatalf("recorded %d spans, want 1", len(spans))
		}
		got := spans[0]
		if got.Name() != "Login" {
			t.Errorf("span name = %q, want %q", got.Name(), "Login")
		}
		if got.Status().Code.String() != "Error" {
			t.Errorf("error path should set status Error, got %q",
				got.Status().Code.String())
		}
		if len(got.Events()) == 0 {
			t.Errorf("RecordError should have added an event; got none")
		}
	})
}

// hasStringAttr returns whether the span carries the given string
// attribute key=val pair. Avoids pulling attribute.KeyValue equality
// into every assertion.
func hasStringAttr(span sdktrace.ReadOnlySpan, key, val string) bool {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key && kv.Value.AsString() == val {
			return true
		}
	}
	return false
}
