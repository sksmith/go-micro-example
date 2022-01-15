package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/queue"

	"github.com/go-chi/chi"
	"github.com/pkg/errors"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/db/invrepo"
)

func configureServer(s inventory.Service) *httptest.Server {
	r := chi.NewRouter()

	invApi := api.NewInventoryApi(s)
	invApi.ConfigureRouter(r)

	return httptest.NewServer(r)
}

func TestList(t *testing.T) {
	mockRepo := invrepo.NewMockRepo()
	mockQueue := queue.NewMockQueue()

	mockRepo.GetAllProductInventoryFunc = func(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]inventory.ProductInventory, error) {
		products := make([]inventory.ProductInventory, 2)
		products[0] = testProductInventory[0]
		products[1] = testProductInventory[2]
		return products, nil
	}

	s := inventory.NewService(mockRepo, mockQueue)
	ts := configureServer(s)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}

	body, err := ioutil.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("%s", body)
}

func TestListError(t *testing.T) {
	mockRepo := invrepo.NewMockRepo()
	mockQueue := queue.NewMockQueue()

	mockRepo.GetAllProductInventoryFunc = func(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]inventory.ProductInventory, error) {
		return nil, errors.New("some terrible error has occurred in the db")
	}

	service := inventory.NewService(mockRepo, mockQueue)
	ts := configureServer(service)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}

	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("Status Code got=%d want=%d", res.StatusCode, http.StatusInternalServerError)
	}
}

func TestPagination(t *testing.T) {
	mockRepo := invrepo.NewMockRepo()
	mockQueue := queue.NewMockQueue()

	wantLimit := 10
	wantOffset := 50

	mockRepo.GetAllProductInventoryFunc = func(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]inventory.ProductInventory, error) {
		if limit != wantLimit {
			t.Errorf("limit got=%d want=%d", limit, wantLimit)
		}
		if offset != wantOffset {
			t.Errorf("limit got=%d want=%d", offset, wantOffset)
		}

		return nil, nil
	}

	service := inventory.NewService(mockRepo, mockQueue)
	ts := configureServer(service)
	defer ts.Close()

	_, err := http.Get(ts.URL + fmt.Sprintf("/v1?limit=%d&offset=%d", wantLimit, wantOffset))
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreate(t *testing.T) {
	mockRepo := invrepo.NewMockRepo()
	mockQueue := queue.NewMockQueue()

	tp := testProducts[0]

	mockRepo.SaveProductFunc = func(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error {
		if product.Name != tp.Name {
			t.Errorf("name got=%s want=%s", product.Name, tp.Name)
		}
		if product.Sku != tp.Sku {
			t.Errorf("sku got=%s want=%s", product.Sku, tp.Sku)
		}
		if product.Upc != tp.Upc {
			t.Errorf("upc got=%s want=%s", product.Upc, tp.Upc)
		}
		return nil
	}

	service := inventory.NewService(mockRepo, mockQueue)
	ts := configureServer(service)
	defer ts.Close()

	data, err := json.Marshal(tp)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(ts.URL+"/v1", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	body, err := ioutil.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("%s", body)
}

func TestCreateProductionEvent(t *testing.T) {
	mockRepo := invrepo.NewMockRepo()
	mockQueue := queue.NewMockQueue()

	tpe := testProductionEvents[0]

	mockRepo.GetProductFunc = func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
		return testProducts[0], nil
	}

	mockRepo.GetProductionEventByRequestIDFunc = func(ctx context.Context, requestID string, options ...core.QueryOptions) (pe inventory.ProductionEvent, err error) {
		return pe, core.ErrNotFound
	}

	mockRepo.SaveProductionEventFunc = func(ctx context.Context, event *inventory.ProductionEvent, options ...core.UpdateOptions) error {
		if event.ID != 0 {
			t.Errorf("id should be ignored on creation got=%d want=%d", event.ID, 0)
		}
		if event.Sku != tpe.Sku {
			t.Errorf("sku got=%s want=%s", event.Sku, tpe.Sku)
		}
		if event.Created == tpe.Created {
			t.Errorf("event created should be set upon creation")
		}
		if event.Quantity != tpe.Quantity {
			t.Errorf("quantity got=%d want=%d", event.Quantity, tpe.Quantity)
		}
		return nil
	}

	service := inventory.NewService(mockRepo, mockQueue)
	ts := configureServer(service)
	defer ts.Close()

	data, err := json.Marshal(tpe)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("PUT", ts.URL+fmt.Sprintf("/v1/%s/productionEvent", tpe.Sku),
		bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if res.StatusCode != 201 {
		t.Errorf("unexpected status code got=%d want=%d", res.StatusCode, 201)
	}

	body, err := ioutil.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	resp := &api.ProductionEventResponse{}
	err = json.Unmarshal(body, resp)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateProductNotFound(t *testing.T) {
	mockRepo := invrepo.NewMockRepo()
	mockQueue := queue.NewMockQueue()

	tpe := testProductionEvents[0]

	mockRepo.GetProductFunc = func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
		if sku != tpe.Sku {
			t.Errorf("sku got=%s want=%s", sku, tpe.Sku)
		}
		return inventory.Product{}, core.ErrNotFound
	}

	service := inventory.NewService(mockRepo, mockQueue)
	ts := configureServer(service)
	defer ts.Close()

	data, err := json.Marshal(tpe)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(ts.URL+fmt.Sprintf("/v1/%s/productionEvent", tpe.Sku),
		"application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	if res.StatusCode != 404 {
		t.Errorf("unexpected StatusCode got=%d want=%d", res.StatusCode, 404)
	}
}

func TestCreateReservation(t *testing.T) {
	mockRepo := invrepo.NewMockRepo()
	mockQueue := queue.NewMockQueue()

	trr := testReservationRequests[0]
	tr := testReservations[0]
	tp := testProducts[0]

	mockRepo.GetProductFunc = func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
		return tp, nil
	}

	mockRepo.SaveReservationFunc = func(ctx context.Context, r *inventory.Reservation, options ...core.UpdateOptions) error {
		if r.ID != 0 {
			t.Errorf("id should be ignored on creation got=%d want=%d", r.ID, 0)
		}
		if r.Requester != trr.Requester {
			t.Errorf("requester got=%s want=%s", r.Requester, trr.Requester)
		}
		if r.Sku != tp.Sku {
			t.Errorf("sku got=%s want=%s", r.Sku, tp.Sku)
		}
		if r.State != inventory.Open {
			t.Errorf("state got=%s want=%s", r.State, inventory.Open)
		}
		if r.ReservedQuantity != 0 {
			t.Errorf("reserved quantity should be ignored on creation got=%d want=%d", r.ReservedQuantity, trr.Quantity)
		}
		if r.RequestedQuantity != trr.Quantity {
			t.Errorf("requestedQuantity got=%d want=%d", r.RequestedQuantity, trr.Quantity)
		}
		return nil
	}

	mockRepo.GetSkuReservesByStateFunc =
		func(ctx context.Context, sku string, state inventory.ReserveState, limit, offset int,
			options ...core.QueryOptions) ([]inventory.Reservation, error) {

			return []inventory.Reservation{tr}, nil
		}

	mockRepo.UpdateReservationFunc =
		func(ctx context.Context, ID uint64, state inventory.ReserveState, qty int64, options ...core.UpdateOptions) error {
			if ID != tr.ID {
				t.Errorf("id got=%d want=%d", ID, tr.ID)
			}
			if state != inventory.Closed {
				t.Errorf("state got=%s want=%s", state, inventory.Closed)
			}
			if qty != tr.RequestedQuantity {
				t.Errorf("reservedQuantity got=%d want=%d", qty, tr.RequestedQuantity)
			}
			return nil
		}

	mockRepo.GetProductInventoryFunc =
		func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.ProductInventory, error) {
			if sku != "sku1" {
				t.Errorf("sku got=%s want=%s", sku, "sku1")
			}

			return inventory.ProductInventory{
				Product: inventory.Product{
					Sku: "sku1",
				},
				Available: 10,
			}, nil
		}

	sentToQueue := false
	mockQueue.PublishInventoryFunc = func(ctx context.Context, productInventory inventory.ProductInventory) error {
		sentToQueue = true
		return nil
	}

	service := inventory.NewService(mockRepo, mockQueue)
	ts := configureServer(service)
	defer ts.Close()

	data, err := json.Marshal(trr)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("PUT", ts.URL+fmt.Sprintf("/v1/%s/reservation", tr.Sku),
		bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if !sentToQueue {
		t.Errorf("sentToQueue got=%t want=%t", sentToQueue, true)
	}

	if res.StatusCode != 201 {
		t.Errorf("status code got=%d want=%d", res.StatusCode, 201)
	}

	body, err := ioutil.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	resp := &api.ReservationResponse{}
	err = json.Unmarshal(body, resp)
	if err != nil {
		t.Fatal(err)
	}
}

var testProducts = []inventory.Product{
	{
		Sku:  "sku1",
		Upc:  "upc1",
		Name: "name1",
	},
	{
		Sku:  "sku2",
		Upc:  "upc2",
		Name: "name2",
	},
	{
		Sku:  "sku3",
		Upc:  "upc3",
		Name: "name3",
	},
}

var testProductInventory = []inventory.ProductInventory{
	{
		Product:   testProducts[0],
		Available: 1,
	},
	{
		Product:   testProducts[1],
		Available: 1,
	},
	{
		Product:   testProducts[2],
		Available: 1,
	},
}

var testProductionEvents = []inventory.ProductionEvent{
	{
		ID:        0,
		RequestID: "0",
		Sku:       "sku1",
		Quantity:  1,
		Created:   time.Time{},
	},
	{
		ID:        1,
		RequestID: "1",
		Sku:       "sku1",
		Quantity:  1,
		Created:   time.Time{},
	},
}

var testReservationRequests = []inventory.ReservationRequest{
	{
		RequestID: "0",
		Requester: "requester1",
		Quantity:  10,
	},
}

var testReservations = []inventory.Reservation{
	{
		ID:                0,
		RequestID:         "0",
		Requester:         "requester1",
		Sku:               "sku1",
		State:             "state?",
		ReservedQuantity:  0,
		RequestedQuantity: 10,
		Created:           time.Time{},
	},
	{
		ID:                1,
		RequestID:         "1",
		Requester:         "requester2",
		Sku:               "sku2",
		State:             "state?",
		ReservedQuantity:  1,
		RequestedQuantity: 10,
		Created:           time.Time{},
	},
}
