package observability

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Without this hook, zerolog's log.Ctx(ctx) returns its package-level
// disabled logger when ctx has no logger attached — which silently
// drops logs from any code path reached outside an HTTP request (tests,
// startup, queue consumer cold paths). Pinning DefaultContextLogger to
// the address of the global zerolog/log.Logger keeps log.Ctx(ctx)
// behaving like log.Logger when no request-scoped logger has been
// installed, so callers can use log.Ctx unconditionally.
func init() {
	zerolog.DefaultContextLogger = &log.Logger
}

// requestIDCtxKey is a private type so the value cannot collide with
// other packages' context keys (including chi's middleware.RequestIDKey,
// which uses a different unexported key on a different package path).
type requestIDCtxKey struct{}

// ContextWithRequestID stores the supplied request ID on ctx so that
// transports without an HTTP request (e.g. the AMQP consumer) can
// re-establish correlation by extracting it back out via
// RequestIDFromContext. An empty id is a no-op so callers don't need
// to guard the empty case themselves.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDCtxKey{}, id)
}

// RequestIDFromContext returns the request ID stored by
// ContextWithRequestID, or "" if none is present.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDCtxKey{}).(string); ok {
		return v
	}
	return ""
}
