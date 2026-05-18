// SEC-010: redaction helpers used by the request logger so query
// strings cannot leak bearer tokens, OAuth codes, password resets,
// or other credential-like values into application logs.
//
// Authorization headers and cookies are not logged anywhere in this
// service today (the Logging middleware only emits the URI and the
// Origin header). RedactURI exists so they cannot be leaked via the
// other end of the request — `?token=…` style query strings, which
// are easy to write by accident and the canonical example in the
// OWASP Logging Cheat Sheet.
package httpx

import (
	"net/url"
	"strings"
)

// redactedPlaceholder is what a sensitive query value is replaced
// with in logged URIs. The string is short and obvious so log
// scrapers and humans both recognise the redaction at a glance.
const redactedPlaceholder = "[REDACTED]"

// SensitiveQueryParams is the documented denylist of query-string
// keys whose VALUES must never appear in a log line. Comparison is
// case-insensitive — a `?Token=…` is still redacted. Add to this
// list (not silently in code) when a new sensitive identifier is
// introduced so the policy stays auditable.
//
// Categories covered:
//   - OAuth / OIDC: token, access_token, refresh_token, id_token, code, state
//   - Auth credentials: password, passwd, secret, api_key, apikey, authorization
//   - Session: session, sessionid, sid
//
// Keep entries lowercase — the lookup lowercases before matching.
var SensitiveQueryParams = map[string]struct{}{
	"token":         {},
	"access_token":  {},
	"refresh_token": {},
	"id_token":      {},
	"code":          {},
	"state":         {},
	"password":      {},
	"passwd":        {},
	"secret":        {},
	"api_key":       {},
	"apikey":        {},
	"authorization": {},
	"session":       {},
	"sessionid":     {},
	"sid":           {},
}

// RedactURI renders a URL safe for logging. The path is preserved,
// query keys are preserved, but values for keys in
// SensitiveQueryParams are replaced with [REDACTED]. Fragments are
// dropped because they never appear in server-side request URLs
// anyway. A nil URL returns the empty string.
//
// The result is not parseable back to the original URL — it is
// intentionally lossy on the redacted values.
func RedactURI(u *url.URL) string {
	if u == nil {
		return ""
	}

	if u.RawQuery == "" {
		return u.Path
	}

	q := u.Query()
	for key, values := range q {
		if _, sensitive := SensitiveQueryParams[strings.ToLower(key)]; !sensitive {
			continue
		}
		for i := range values {
			values[i] = redactedPlaceholder
		}
		q[key] = values
	}

	// url.Values.Encode sorts keys for stable output, which is also
	// what we want for log diffability across runs.
	return u.Path + "?" + q.Encode()
}
