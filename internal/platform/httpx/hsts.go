package httpx

import (
	"net/http"
	"strconv"
)

// defaultHSTSMaxAge is one year in seconds, which is the value the
// HSTS preload list requires and what Mozilla's Server Side TLS
// "intermediate" profile recommends. Shorter values still satisfy the
// header spec but lose preload eligibility.
const defaultHSTSMaxAge = 31536000

// HSTSOptions tunes the Strict-Transport-Security header. Defaults
// match Mozilla's intermediate TLS profile: one-year max-age,
// includeSubDomains on, preload off (callers opt in once they've
// confirmed every subdomain is HTTPS-only).
type HSTSOptions struct {
	MaxAgeSeconds     int
	IncludeSubDomains bool
	Preload           bool
}

// HSTS returns middleware that emits Strict-Transport-Security only on
// requests that arrived over TLS — either directly (r.TLS != nil) or
// through an HTTPS-terminating proxy that set X-Forwarded-Proto: https.
// The header is meaningless (and discarded by browsers) on plaintext
// responses, so we don't bother emitting it there; this also keeps
// local plaintext dev free of header noise.
func HSTS(opts HSTSOptions) func(http.Handler) http.Handler {
	if opts.MaxAgeSeconds <= 0 {
		opts.MaxAgeSeconds = defaultHSTSMaxAge
	}
	value := buildHSTSValue(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isHTTPSRequest(r) {
				w.Header().Set("Strict-Transport-Security", value)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isHTTPSRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

func buildHSTSValue(opts HSTSOptions) string {
	v := "max-age=" + strconv.Itoa(opts.MaxAgeSeconds)
	if opts.IncludeSubDomains {
		v += "; includeSubDomains"
	}
	if opts.Preload {
		v += "; preload"
	}
	return v
}
