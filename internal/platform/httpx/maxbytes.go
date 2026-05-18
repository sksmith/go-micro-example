package httpx

import (
	"net/http"
	"strconv"
)

// DefaultMaxRequestBodyBytes is the body-size cap applied when the
// caller does not override it. 1 MiB is comfortable for every JSON
// DTO this service accepts (the largest request bodies are a few
// hundred bytes) and small enough that an attacker cannot exhaust
// memory by streaming megabyte-scale payloads to the auth or write
// paths.
const DefaultMaxRequestBodyBytes int64 = 1 << 20

// MaxBytes returns a middleware that caps the request body to limit
// bytes. Two enforcement points:
//
//  1. A pre-check on Content-Length: requests that declare a body
//     larger than the limit are rejected with 413
//     application/problem+json before any bytes are read. This is
//     the common case (well-behaved clients announce their size).
//  2. For chunked or missing-Content-Length requests, r.Body is
//     wrapped with http.MaxBytesReader so downstream Decode/ReadAll
//     calls hit the cap when they cross it. MaxBytesReader sets
//     Connection: close on overflow so the offending stream is
//     terminated rather than allowed to keep consuming buffer.
//
// Bodyless methods (GET/HEAD/DELETE/OPTIONS) skip the middleware so
// a stray Content-Length header on those requests is not treated as
// an oversize body. A non-positive limit disables the middleware
// entirely.
func MaxBytes(limit int64) func(http.Handler) http.Handler {
	if limit <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !methodCarriesBody(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if r.ContentLength > limit {
				writeMaxBytesProblem(w, r, limit)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

func methodCarriesBody(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodDelete, http.MethodOptions:
		return false
	}
	return true
}

func writeMaxBytesProblem(w http.ResponseWriter, r *http.Request, limit int64) {
	(&Problem{
		Title:  http.StatusText(http.StatusRequestEntityTooLarge),
		Status: http.StatusRequestEntityTooLarge,
		Detail: "request body exceeds " + strconv.FormatInt(limit, 10) + " bytes",
	}).WriteTo(w, r)
}
