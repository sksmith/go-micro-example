package api

import (
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog/log"
)

// Problem is the RFC 7807 (application/problem+json) error envelope.
// Every API error path renders one. Type defaults to "about:blank";
// callers may set a more specific URI when they want to advertise a
// stable, documentable error category.
type Problem struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Status   int            `json:"status"`
	Detail   string         `json:"detail,omitempty"`
	Instance string         `json:"instance,omitempty"`
	Errors   []FieldProblem `json:"errors,omitempty"`

	// Err is the underlying error captured for logging; never serialized.
	Err error `json:"-"`
}

// FieldProblem is a validation-error extension entry.
type FieldProblem struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// Render satisfies the render.Renderer interface but is a no-op:
// problems are emitted via WriteTo so the application/problem+json
// content type is not overwritten by go-chi/render's JSON responder.
// Always route problems through api.Render — never render.Render —
// so the WriteTo path is taken.
func (*Problem) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

// WriteTo serializes the problem to the response per RFC 7807 §3.
func (p *Problem) WriteTo(w http.ResponseWriter, r *http.Request) {
	if p.Type == "" {
		p.Type = "about:blank"
	}
	if p.Instance == "" {
		p.Instance = r.URL.Path
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		log.Ctx(r.Context()).Warn().Err(err).Msg("failed to encode problem")
	}
}

func BadRequestProblem(err error) *Problem {
	return &Problem{
		Type:   "about:blank",
		Title:  http.StatusText(http.StatusBadRequest),
		Status: http.StatusBadRequest,
		Detail: err.Error(),
		Err:    err,
	}
}

func ValidationProblem(fields ...FieldProblem) *Problem {
	return &Problem{
		Type:   "about:blank",
		Title:  http.StatusText(http.StatusBadRequest),
		Status: http.StatusBadRequest,
		Detail: "request validation failed",
		Errors: fields,
	}
}

// NotFoundProblem returns a fresh problem each call so concurrent
// requests cannot race on a shared Instance field.
func NotFoundProblem() *Problem {
	return &Problem{
		Type:   "about:blank",
		Title:  http.StatusText(http.StatusNotFound),
		Status: http.StatusNotFound,
	}
}

// InternalServerProblem returns a generic 500. Per DSN-010, the body
// never contains the wrapped error string or a stack trace — those are
// captured on Err for logging only.
func InternalServerProblem(err error) *Problem {
	return &Problem{
		Type:   "about:blank",
		Title:  http.StatusText(http.StatusInternalServerError),
		Status: http.StatusInternalServerError,
		Detail: "An internal server error occurred.",
		Err:    err,
	}
}
