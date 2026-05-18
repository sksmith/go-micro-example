package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sksmith/go-micro-example/internal/platform/httpx"
)

// Pagination is the validated representation of `?limit=…&offset=…`
// that list handlers receive. DSN-011 made this typed and explicit
// instead of stashing raw ints under string-keyed context values.
type Pagination struct {
	Limit  int
	Offset int
}

const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

type pageCtxKey struct{}

// Paginate validates `limit` and `offset` query params. Invalid input
// is rejected with an RFC 7807 problem+json 400 — callers no longer
// receive silently-coerced defaults (which used to mask client bugs).
func Paginate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, problem := parsePagination(r.URL.Query())
		if problem != nil {
			httpx.Render(w, r, problem)
			return
		}
		ctx := context.WithValue(r.Context(), pageCtxKey{}, p)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// PaginationFrom retrieves the validated Pagination set by Paginate.
// Returns the zero/default value when the middleware was not mounted,
// so handlers in non-paginated routes still get sane defaults if they
// reach for it.
func PaginationFrom(ctx context.Context) Pagination {
	if p, ok := ctx.Value(pageCtxKey{}).(Pagination); ok {
		return p
	}
	return Pagination{Limit: DefaultPageLimit}
}

func parsePagination(q url.Values) (Pagination, *httpx.Problem) {
	p := Pagination{Limit: DefaultPageLimit}
	var fields []httpx.FieldProblem

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		switch {
		case err != nil, n < 1:
			fields = append(fields, httpx.FieldProblem{Field: "limit", Detail: "must be a positive integer"})
		case n > MaxPageLimit:
			fields = append(fields, httpx.FieldProblem{Field: "limit", Detail: fmt.Sprintf("must be ≤ %d", MaxPageLimit)})
		default:
			p.Limit = n
		}
	}

	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			fields = append(fields, httpx.FieldProblem{Field: "offset", Detail: "must be a non-negative integer"})
		} else {
			p.Offset = n
		}
	}

	if len(fields) > 0 {
		return Pagination{}, httpx.ValidationProblem(fields...)
	}
	return p, nil
}

// WriteLinkHeader emits an RFC 8288 Link header with `next` and `prev`
// rels for offset-based pagination. `returnedCount` is the size of the
// page actually served; when it equals limit, a `next` link is added
// (we can't know without counting whether more truly exist, but
// returning a full page is the conventional signal that another page
// is probably available).
func WriteLinkHeader(w http.ResponseWriter, r *http.Request, p Pagination, returnedCount int) {
	var links []string

	if returnedCount == p.Limit {
		links = append(links, formatLink(r, p.Limit, p.Offset+p.Limit, "next"))
	}
	if p.Offset > 0 {
		prev := p.Offset - p.Limit
		if prev < 0 {
			prev = 0
		}
		links = append(links, formatLink(r, p.Limit, prev, "prev"))
	}

	if len(links) > 0 {
		w.Header().Set("Link", strings.Join(links, ", "))
	}
}

func formatLink(r *http.Request, limit, offset int, rel string) string {
	u := *r.URL
	q := u.Query()
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	u.RawQuery = q.Encode()
	return fmt.Sprintf(`<%s>; rel=%q`, u.RequestURI(), rel)
}
