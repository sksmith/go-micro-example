package observability_test

import (
	"context"
	"testing"

	"github.com/sksmith/go-micro-example/core/observability"
)

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := observability.ContextWithRequestID(context.Background(), "abc-123")
	if got := observability.RequestIDFromContext(ctx); got != "abc-123" {
		t.Errorf("round-trip got %q, want %q", got, "abc-123")
	}
}

func TestRequestIDFromContext_Missing(t *testing.T) {
	if got := observability.RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("expected empty string from bare context, got %q", got)
	}
}

func TestContextWithRequestID_EmptyIsNoOp(t *testing.T) {
	parent := context.Background()
	ctx := observability.ContextWithRequestID(parent, "")
	// Empty IDs should not be stored (so RequestIDFromContext returns ""
	// rather than the empty string we'd otherwise have written).
	if got := observability.RequestIDFromContext(ctx); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
