package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
)

func paginationTestServer(t *testing.T) (*httptest.Server, *inventory.MockInventoryService) {
	t.Helper()
	mockSvc := inventory.NewMockInventoryService()
	invApi := api.NewInventoryApi(mockSvc)
	r := chi.NewRouter()
	invApi.ConfigureRouter(r)
	return httptest.NewServer(r), mockSvc
}

func TestPaginationRejectsInvalidInput(t *testing.T) {
	ts, _ := paginationTestServer(t)
	defer ts.Close()

	tests := []struct {
		name  string
		query string
		field string
	}{
		{"non-numeric limit", "limit=abc", "limit"},
		{"zero limit", "limit=0", "limit"},
		{"negative limit", "limit=-1", "limit"},
		{"limit over max", "limit=" + strconv.Itoa(api.MaxPageLimit+1), "limit"},
		{"non-numeric offset", "offset=xyz", "offset"},
		{"negative offset", "offset=-5", "offset"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := http.Get(ts.URL + "/?" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status got=%d want=400", res.StatusCode)
			}
			if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
				t.Errorf("content-type got=%q", got)
			}
			body, _ := io.ReadAll(res.Body)
			var p httpx.Problem
			if err := json.Unmarshal(body, &p); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(p.Errors) == 0 {
				t.Fatalf("expected field-level errors[] extension, got %+v", p)
			}
			found := false
			for _, fe := range p.Errors {
				if fe.Field == tc.field {
					found = true
				}
			}
			if !found {
				t.Errorf("expected error on field %q, got %+v", tc.field, p.Errors)
			}
		})
	}
}

func TestPaginationDefaultsAndClamp(t *testing.T) {
	ts, mockSvc := paginationTestServer(t)
	defer ts.Close()

	var gotLimit, gotOffset int
	mockSvc.GetAllProductInventoryFunc = func(ctx context.Context, limit, offset int) ([]inventory.ProductInventory, error) {
		gotLimit, gotOffset = limit, offset
		return nil, nil
	}

	// No query params — defaults apply.
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if gotLimit != api.DefaultPageLimit {
		t.Errorf("default limit got=%d want=%d", gotLimit, api.DefaultPageLimit)
	}
	if gotOffset != 0 {
		t.Errorf("default offset got=%d want=0", gotOffset)
	}

	// Max-boundary limit accepted.
	res, err = http.Get(ts.URL + "/?limit=" + strconv.Itoa(api.MaxPageLimit))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if gotLimit != api.MaxPageLimit {
		t.Errorf("boundary limit got=%d want=%d", gotLimit, api.MaxPageLimit)
	}
}

func TestPaginationLinkHeader(t *testing.T) {
	ts, mockSvc := paginationTestServer(t)
	defer ts.Close()

	t.Run("full page emits next, no prev when offset=0", func(t *testing.T) {
		mockSvc.GetAllProductInventoryFunc = func(ctx context.Context, limit, offset int) ([]inventory.ProductInventory, error) {
			return make([]inventory.ProductInventory, limit), nil
		}
		res, err := http.Get(ts.URL + "/?limit=10")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		link := res.Header.Get("Link")
		if !strings.Contains(link, `rel="next"`) {
			t.Errorf("expected next rel in Link header, got %q", link)
		}
		if strings.Contains(link, `rel="prev"`) {
			t.Errorf("did not expect prev at offset=0, got %q", link)
		}
		if !strings.Contains(link, "offset=10") {
			t.Errorf("next link should point at offset=10, got %q", link)
		}
	})

	t.Run("middle page emits both next and prev", func(t *testing.T) {
		mockSvc.GetAllProductInventoryFunc = func(ctx context.Context, limit, offset int) ([]inventory.ProductInventory, error) {
			return make([]inventory.ProductInventory, limit), nil
		}
		res, err := http.Get(ts.URL + "/?limit=10&offset=20")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		link := res.Header.Get("Link")
		if !strings.Contains(link, `rel="next"`) || !strings.Contains(link, `rel="prev"`) {
			t.Errorf("expected both next and prev, got %q", link)
		}
		if !strings.Contains(link, "offset=30") {
			t.Errorf("next should be offset=30, got %q", link)
		}
		if !strings.Contains(link, "offset=10") {
			t.Errorf("prev should be offset=10, got %q", link)
		}
	})

	t.Run("partial page omits next", func(t *testing.T) {
		mockSvc.GetAllProductInventoryFunc = func(ctx context.Context, limit, offset int) ([]inventory.ProductInventory, error) {
			return make([]inventory.ProductInventory, limit-1), nil
		}
		res, err := http.Get(ts.URL + "/?limit=10&offset=10")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		link := res.Header.Get("Link")
		if strings.Contains(link, `rel="next"`) {
			t.Errorf("did not expect next on partial page, got %q", link)
		}
		if !strings.Contains(link, `rel="prev"`) {
			t.Errorf("expected prev on second page, got %q", link)
		}
	})
}
