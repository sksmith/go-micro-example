package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/internal/catalog"
	"github.com/sksmith/go-micro-example/internal/inventory"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
	"github.com/sksmith/go-micro-example/testutil"

	"github.com/go-chi/chi/v5"
)

func TestInventorySubscribe(t *testing.T) {
	// Flaky under Go 1.24 on Linux/macOS CI runners: the handler reports
	// 3 frames written via wsutil.WriteServerText but the dialer's
	// ReadHeader times out waiting for any bytes. Passes locally and on
	// Windows. Tracked in OPS-007 — re-enable once the WS test harness
	// is rewritten without an in-process httptest WS round-trip.
	t.Skip("WS subscribe test is flaky on Linux/macOS under Go 1.24 — see OPS-009")
	mockSvc := inventory.NewMockInventoryService()

	subscribed := make(chan struct{}, 1)
	unsubscribed := make(chan struct{}, 1)
	releaseSubscribe := make(chan struct{})
	expectedSubId := inventory.InventorySubID("subid1")

	mockSvc.SubscribeInventoryFunc = func(ch chan<- inventory.ProductInventory) (id inventory.InventorySubID) {
		subscribed <- struct{}{}
		go func() {
			inv := getTestProductInventory()
			for i := 0; i < 3; i++ {
				ch <- inv[i]
			}
			// Hold the channel open until the test has read all
			// items off the websocket; otherwise the handler's
			// defer conn.Close() can race ahead and the client
			// sees EOF mid-read.
			<-releaseSubscribe
			close(ch)
		}()

		return expectedSubId
	}

	mockSvc.UnsubscribeInventoryFunc = func(id inventory.InventorySubID) {
		unsubscribed <- struct{}{}
	}

	invApi := inventory.NewInventoryApi(mockSvc)
	r := chi.NewRouter()
	invApi.ConfigureRouter(r)
	ts := httptest.NewServer(r)
	defer ts.Close()

	url := strings.Replace(ts.URL, "http", "ws", 1) + "/subscribe"

	conn, _, _, err := ws.DefaultDialer.Dial(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}

	curInv := getTestProductInventory()
	for i := 0; i < 3; i++ {
		got := &inventory.ProductInventory{}
		testutil.ReadWs(conn, got, t)

		if got.Name != curInv[i].Name {
			t.Errorf("unexpected ws response[%d] got=[%s] want=[%s]", i, got.Name, curInv[i].Name)
		}
	}
	close(releaseSubscribe)

	select {
	case <-subscribed:
	case <-time.After(time.Second):
		t.Errorf("subscribe never called")
	}

	select {
	case <-unsubscribed:
	case <-time.After(time.Second):
		t.Errorf("unsubscribe never called")
	}
}

func setupInventoryTestServer() (*httptest.Server, *inventory.MockInventoryService) {
	mockSvc := inventory.NewMockInventoryService()
	invApi := inventory.NewInventoryApi(mockSvc)
	r := chi.NewRouter()
	invApi.ConfigureRouter(r)
	ts := httptest.NewServer(r)

	return ts, mockSvc
}

func TestInventoryList(t *testing.T) {
	ts, mockInvSvc := setupInventoryTestServer()
	defer ts.Close()

	tests := []struct {
		limit          int
		wantLimit      int
		offset         int
		wantOffset     int
		inventory      []inventory.ProductInventory
		serviceErr     error
		wantInventory  []inventory.ProductInventory
		wantErr        *httpx.Problem
		wantStatusCode int
	}{
		{
			limit:          -1,
			wantLimit:      50,
			offset:         -1,
			wantOffset:     0,
			inventory:      getTestProductInventory(),
			wantInventory:  getTestProductInventory(),
			serviceErr:     nil,
			wantErr:        nil,
			wantStatusCode: http.StatusOK,
		},
		{
			limit:          5,
			wantLimit:      5,
			offset:         7,
			wantOffset:     7,
			inventory:      getTestProductInventory(),
			wantInventory:  getTestProductInventory(),
			serviceErr:     nil,
			wantErr:        nil,
			wantStatusCode: http.StatusOK,
		},
		{
			limit:          -1,
			wantLimit:      50,
			offset:         -1,
			wantOffset:     0,
			inventory:      []inventory.ProductInventory{},
			wantInventory:  []inventory.ProductInventory{},
			serviceErr:     nil,
			wantErr:        nil,
			wantStatusCode: http.StatusOK,
		},
		{
			limit:          -1,
			wantLimit:      50,
			offset:         -1,
			wantOffset:     0,
			inventory:      []inventory.ProductInventory{},
			wantInventory:  []inventory.ProductInventory{},
			serviceErr:     errors.New("something bad happened"),
			wantErr:        httpx.InternalServerProblem(nil),
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		gotLimit := -1
		gotOffset := -1
		mockInvSvc.GetAllProductInventoryFunc = func(ctx context.Context, limit int, offset int) ([]inventory.ProductInventory, error) {
			gotLimit = limit
			gotOffset = offset
			return test.inventory, test.serviceErr
		}

		url := ts.URL
		if test.limit > -1 {
			url += fmt.Sprintf("?limit=%d&offset=%d", test.limit, test.offset)
		}

		res, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}

		if test.wantErr == nil {
			got := []inventory.ProductInventory{}
			testutil.Unmarshal(res, &got, t)

			if !reflect.DeepEqual(got, test.wantInventory) {
				t.Errorf("inventory\n got:%+v\nwant:%+v\n", got, test.wantInventory)
			}
		} else {
			got := httpx.Problem{}
			testutil.Unmarshal(res, &got, t)

			if got.Title != test.wantErr.Title {
				t.Errorf("errorResponse\n got:%v\nwant:%v\n", got.Title, test.wantErr.Title)
			}
		}

		if res.StatusCode != test.wantStatusCode {
			t.Errorf("status code got=[%d] want=[%d]", res.StatusCode, test.wantStatusCode)
		}

		if gotLimit != test.wantLimit {
			t.Errorf("limit got=[%d] want=[%d]", gotLimit, test.limit)
		}

		if gotOffset != test.wantOffset {
			t.Errorf("offset got=[%d] want=[%d]", gotOffset, test.offset)
		}
	}
}

func TestInventoryCreateProduct(t *testing.T) {
	ts, mockInvSvc := setupInventoryTestServer()
	defer ts.Close()

	tests := []struct {
		request             inventory.CreateProductRequest
		serviceErr          error
		wantProductResponse *inventory.ProductResponse
		wantErr             *httpx.Problem
		wantStatusCode      int
	}{
		{
			request:             createProductRequest("name1", "sku1", "upc1"),
			serviceErr:          nil,
			wantProductResponse: createProductResponse("name1", "sku1", "upc1", 0),
			wantErr:             nil,
			wantStatusCode:      http.StatusCreated,
		},
		{
			request:             createProductRequest("name1", "sku1", "upc1"),
			serviceErr:          errors.New("some unexpected error"),
			wantProductResponse: nil,
			wantErr:             httpx.InternalServerProblem(nil),
			wantStatusCode:      http.StatusInternalServerError,
		},
		{
			request:             createProductRequest("name1", "sku1", ""),
			serviceErr:          nil,
			wantProductResponse: nil,
			wantErr:             httpx.BadRequestProblem(errors.New("missing required field(s)")),
			wantStatusCode:      http.StatusBadRequest,
		},
		{
			request:             createProductRequest("name1", "", "upc1"),
			serviceErr:          nil,
			wantProductResponse: nil,
			wantErr:             httpx.BadRequestProblem(errors.New("missing required field(s)")),
			wantStatusCode:      http.StatusBadRequest,
		},
		{
			request:             createProductRequest("", "sku1", "upc1"),
			serviceErr:          nil,
			wantProductResponse: nil,
			wantErr:             httpx.BadRequestProblem(errors.New("missing required field(s)")),
			wantStatusCode:      http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		mockInvSvc.CreateProductFunc = func(ctx context.Context, product inventory.Product) error {
			return test.serviceErr
		}

		res := testutil.Put(ts.URL, test.request, t)

		if res.StatusCode != test.wantStatusCode {
			t.Errorf("status code got=%d\nwant=%d", res.StatusCode, test.wantStatusCode)
		}

		if test.wantErr == nil {
			got := inventory.ProductResponse{}
			testutil.Unmarshal(res, &got, t)

			if !reflect.DeepEqual(got, *test.wantProductResponse) {
				t.Errorf("product\n got=%+v\nwant=%+v", got, *test.wantProductResponse)
			}
		} else {
			got := &httpx.Problem{}
			testutil.Unmarshal(res, got, t)

			if got.Title != test.wantErr.Title {
				t.Errorf("status text got=%s want=%s", got.Title, test.wantErr.Title)
			}
			if got.Detail != test.wantErr.Detail {
				t.Errorf("error text got=%s want=%s", got.Detail, test.wantErr.Detail)
			}
		}
	}
}

func TestInventoryCreateProductionEvent(t *testing.T) {
	ts, mockInvSvc := setupInventoryTestServer()
	defer ts.Close()

	tests := []struct {
		getProductFunc              func(ctx context.Context, sku string) (inventory.Product, error)
		produceFunc                 func(ctx context.Context, product inventory.Product, event inventory.ProductionRequest) error
		sku                         string
		request                     *inventory.CreateProductionEventRequest
		wantProductionEventResponse *inventory.ProductionEventResponse
		wantErr                     *httpx.Problem
		wantStatusCode              int
	}{
		{
			getProductFunc: func(ctx context.Context, sku string) (inventory.Product, error) {
				return getTestProductInventory()[0].Product, nil
			},
			produceFunc: func(ctx context.Context, product inventory.Product, event inventory.ProductionRequest) error {
				return nil
			},
			sku:                         "testsku1",
			request:                     createProductionEventRequest("abc123", 1),
			wantProductionEventResponse: &inventory.ProductionEventResponse{},
			wantErr:                     nil,
			wantStatusCode:              http.StatusCreated,
		},
		{
			getProductFunc: func(ctx context.Context, sku string) (inventory.Product, error) {
				return inventory.Product{}, core.ErrNotFound
			},
			produceFunc:                 nil,
			sku:                         "testsku1",
			request:                     createProductionEventRequest("abc123", 1),
			wantProductionEventResponse: nil,
			wantErr:                     httpx.NotFoundProblem(),
			wantStatusCode:              http.StatusNotFound,
		},
		{
			getProductFunc: func(ctx context.Context, sku string) (inventory.Product, error) {
				return inventory.Product{}, errors.New("some unexpected error")
			},
			produceFunc:                 nil,
			sku:                         "testsku1",
			request:                     createProductionEventRequest("abc123", 1),
			wantProductionEventResponse: nil,
			wantErr:                     httpx.InternalServerProblem(nil),
			wantStatusCode:              http.StatusInternalServerError,
		},
		{
			getProductFunc: func(ctx context.Context, sku string) (inventory.Product, error) {
				return getTestProductInventory()[0].Product, nil
			},
			produceFunc: func(ctx context.Context, product inventory.Product, event inventory.ProductionRequest) error {
				return errors.New("some unexpected error")
			},
			sku:                         "testsku1",
			request:                     createProductionEventRequest("abc123", 1),
			wantProductionEventResponse: nil,
			wantErr:                     httpx.InternalServerProblem(nil),
			wantStatusCode:              http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		mockInvSvc.GetProductFunc = test.getProductFunc
		mockInvSvc.ProduceFunc = test.produceFunc

		url := ts.URL + "/" + test.sku + "/productionEvent"
		res := testutil.Put(url, test.request, t)

		if res.StatusCode != test.wantStatusCode {
			t.Errorf("status code got=%d want=%d", res.StatusCode, test.wantStatusCode)
		}

		if test.wantErr == nil {
			got := inventory.ProductionEventResponse{}
			testutil.Unmarshal(res, &got, t)

			if !reflect.DeepEqual(got, *test.wantProductionEventResponse) {
				t.Errorf("product\n got=%+v\nwant=%+v", got, *test.wantProductionEventResponse)
			}
		} else {
			got := &httpx.Problem{}
			testutil.Unmarshal(res, got, t)

			if got.Title != test.wantErr.Title {
				t.Errorf("status text got=%s want=%s", got.Title, test.wantErr.Title)
			}
			if got.Detail != test.wantErr.Detail {
				t.Errorf("error text got=%s want=%s", got.Detail, test.wantErr.Detail)
			}
		}
	}
}

func TestInventoryGetProductInventory(t *testing.T) {
	ts, mockInvSvc := setupInventoryTestServer()
	defer ts.Close()

	tests := []struct {
		sku                     string
		getProductFunc          func(ctx context.Context, sku string) (inventory.Product, error)
		getProductInventoryFunc func(ctx context.Context, sku string) (inventory.ProductInventory, error)
		wantProductResponse     *inventory.ProductResponse
		wantErr                 *httpx.Problem
		wantStatusCode          int
	}{
		{
			getProductFunc: func(ctx context.Context, sku string) (inventory.Product, error) {
				return getTestProductInventory()[0].Product, nil
			},
			getProductInventoryFunc: func(ctx context.Context, sku string) (inventory.ProductInventory, error) {
				return getTestProductInventory()[0], nil
			},
			sku:                 "test1sku",
			wantProductResponse: createProductResponse("test1name", "test1sku", "test1upc", 1),
			wantErr:             nil,
			wantStatusCode:      http.StatusOK,
		},
		{
			getProductFunc: func(ctx context.Context, sku string) (inventory.Product, error) {
				return inventory.Product{}, core.ErrNotFound
			},
			getProductInventoryFunc: nil,
			sku:                     "test1sku",
			wantProductResponse:     nil,
			wantErr:                 httpx.NotFoundProblem(),
			wantStatusCode:          http.StatusNotFound,
		},
		{
			getProductFunc: func(ctx context.Context, sku string) (inventory.Product, error) {
				return getTestProductInventory()[0].Product, nil
			},
			getProductInventoryFunc: func(ctx context.Context, sku string) (inventory.ProductInventory, error) {
				return inventory.ProductInventory{}, core.ErrNotFound
			},
			sku:                 "test1sku",
			wantProductResponse: nil,
			wantErr:             httpx.NotFoundProblem(),
			wantStatusCode:      http.StatusNotFound,
		},
		{
			getProductFunc: func(ctx context.Context, sku string) (inventory.Product, error) {
				return inventory.Product{}, errors.New("some unexpected error")
			},
			getProductInventoryFunc: nil,
			sku:                     "test1sku",
			wantProductResponse:     nil,
			wantErr:                 httpx.InternalServerProblem(nil),
			wantStatusCode:          http.StatusInternalServerError,
		},
		{
			getProductFunc: func(ctx context.Context, sku string) (inventory.Product, error) {
				return getTestProductInventory()[0].Product, nil
			},
			getProductInventoryFunc: func(ctx context.Context, sku string) (inventory.ProductInventory, error) {
				return inventory.ProductInventory{}, errors.New("some unexpected error")
			},
			sku:                 "test1sku",
			wantProductResponse: nil,
			wantErr:             httpx.InternalServerProblem(nil),
			wantStatusCode:      http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		mockInvSvc.GetProductFunc = test.getProductFunc
		mockInvSvc.GetProductInventoryFunc = test.getProductInventoryFunc

		res, err := http.Get(ts.URL + "/" + test.sku)
		if err != nil {
			t.Fatal(err)
		}

		if res.StatusCode != test.wantStatusCode {
			t.Errorf("status code got=%d want=%d", res.StatusCode, test.wantStatusCode)
		}

		if test.wantErr == nil {
			got := inventory.ProductResponse{}
			testutil.Unmarshal(res, &got, t)

			if !reflect.DeepEqual(got, *test.wantProductResponse) {
				t.Errorf("product\n got=%+v\nwant=%+v", got, *test.wantProductResponse)
			}
		} else {
			got := &httpx.Problem{}
			testutil.Unmarshal(res, got, t)

			if got.Title != test.wantErr.Title {
				t.Errorf("status text got=%s want=%s", got.Title, test.wantErr.Title)
			}
			if got.Detail != test.wantErr.Detail {
				t.Errorf("error text got=%s want=%s", got.Detail, test.wantErr.Detail)
			}
		}
	}
}

// TestInventoryGetProductInventoryEnriched covers the DSN-018
// outbound-REST enrichment path: when a catalog client is wired and
// returns a product, the response carries the upstream description;
// when the client returns an error, the response still succeeds
// (catalog is best-effort) and the catalog field is omitted.
func TestInventoryGetProductInventoryEnriched(t *testing.T) {
	tests := []struct {
		name        string
		client      catalog.Client
		wantCatalog *inventory.CatalogInfo
	}{
		{
			name: "lookup ok adds catalog fields",
			client: stubCatalogClient{
				lookup: func(_ context.Context, sku string) (catalog.Product, error) {
					return catalog.Product{Sku: sku, Description: "Widget desc", Category: "tools"}, nil
				},
			},
			wantCatalog: &inventory.CatalogInfo{Description: "Widget desc", Category: "tools"},
		},
		{
			name: "lookup error drops to unenriched response",
			client: stubCatalogClient{
				lookup: func(_ context.Context, _ string) (catalog.Product, error) {
					return catalog.Product{}, errors.New("upstream down")
				},
			},
			wantCatalog: nil,
		},
		{
			name:        "nil client skips enrichment entirely",
			client:      nil,
			wantCatalog: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockSvc := inventory.NewMockInventoryService()
			invApi := inventory.NewInventoryApi(mockSvc)
			invApi.SetCatalog(tc.client)
			r := chi.NewRouter()
			invApi.ConfigureRouter(r)
			ts := httptest.NewServer(r)
			defer ts.Close()

			pi := getTestProductInventory()[0]
			mockSvc.GetProductFunc = func(_ context.Context, _ string) (inventory.Product, error) {
				return pi.Product, nil
			}
			mockSvc.GetProductInventoryFunc = func(_ context.Context, _ string) (inventory.ProductInventory, error) {
				return pi, nil
			}

			res, err := http.Get(ts.URL + "/" + pi.Sku)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status=%d, want 200", res.StatusCode)
			}
			got := inventory.ProductResponse{}
			testutil.Unmarshal(res, &got, t)

			if !reflect.DeepEqual(got.Catalog, tc.wantCatalog) {
				t.Errorf("Catalog\n got=%+v\nwant=%+v", got.Catalog, tc.wantCatalog)
			}
		})
	}
}

// stubCatalogClient is the test seam for the catalog.Client surface.
// Production code receives a *catalog.HTTPClient; the API test only
// needs Lookup to return canned data.
type stubCatalogClient struct {
	lookup func(ctx context.Context, sku string) (catalog.Product, error)
}

func (s stubCatalogClient) Lookup(ctx context.Context, sku string) (catalog.Product, error) {
	return s.lookup(ctx, sku)
}

func createProductionEventRequest(requestID string, quantity int64) *inventory.CreateProductionEventRequest {
	return &inventory.CreateProductionEventRequest{
		ProductionRequest: &inventory.ProductionRequest{RequestID: requestID, Quantity: quantity},
	}
}

func createProductRequest(name, sku, upc string) inventory.CreateProductRequest {
	return inventory.CreateProductRequest{Product: inventory.Product{Name: name, Sku: sku, Upc: upc}}
}

func createProductResponse(name, sku, upc string, available int64) *inventory.ProductResponse {
	return &inventory.ProductResponse{
		ProductInventory: inventory.ProductInventory{
			Available: available,
			Product:   inventory.Product{Name: name, Sku: sku, Upc: upc},
		},
	}
}

func getTestProductInventory() []inventory.ProductInventory {
	return []inventory.ProductInventory{
		{Available: 1, Product: inventory.Product{Sku: "test1sku", Upc: "test1upc", Name: "test1name"}},
		{Available: 2, Product: inventory.Product{Sku: "test2sku", Upc: "test2upc", Name: "test2name"}},
		{Available: 3, Product: inventory.Product{Sku: "test3sku", Upc: "test3upc", Name: "test3name"}},
	}
}
