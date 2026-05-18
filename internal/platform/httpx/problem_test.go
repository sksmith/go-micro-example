package httpx_test

import (
	"net/http"
	"testing"

	"github.com/sksmith/go-micro-example/internal/platform/httpx"
)

func TestValidationProblemCarriesFieldErrors(t *testing.T) {
	p := httpx.ValidationProblem(
		httpx.FieldProblem{Field: "sku", Detail: "required"},
		httpx.FieldProblem{Field: "qty", Detail: "must be > 0"},
	)
	if p.Status != http.StatusBadRequest {
		t.Errorf("status got=%d want=400", p.Status)
	}
	if len(p.Errors) != 2 {
		t.Errorf("errors len got=%d want=2", len(p.Errors))
	}
	if p.Errors[0].Field != "sku" || p.Errors[0].Detail != "required" {
		t.Errorf("field[0] got=%+v", p.Errors[0])
	}
}
