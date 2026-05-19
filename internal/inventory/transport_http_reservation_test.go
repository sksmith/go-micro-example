package inventory_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sksmith/go-micro-example/internal/inventory"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
	"github.com/sksmith/go-micro-example/internal/platform/persistence"
	"github.com/sksmith/go-micro-example/internal/testutil"
)

// TestReservationSubscribe_StreamsThenUnsubscribes is the OPS-009
// replacement for the prior in-process WS round-trip. Same shape as
// TestInventorySubscribe_StreamsThenUnsubscribes; see that test for
// the rationale.
func TestReservationSubscribe_StreamsThenUnsubscribes(t *testing.T) {
	mockSvc := inventory.NewMockReservationService()
	expectedSubID := inventory.ReservationsSubID("subid1")
	items := getTestReservations()

	mockSvc.SubscribeReservationsFunc = func(ch chan<- inventory.Reservation) inventory.ReservationsSubID {
		go func() {
			for _, item := range items {
				ch <- item
			}
			close(ch)
		}()
		return expectedSubID
	}

	var (
		unsubMu sync.Mutex
		unsubID inventory.ReservationsSubID
	)
	mockSvc.UnsubscribeReservationsFunc = func(id inventory.ReservationsSubID) {
		unsubMu.Lock()
		defer unsubMu.Unlock()
		unsubID = id
	}

	w := &recordingTextWriter{}
	inventory.StreamReservationsToClientForTest(mockSvc, w)

	frames := w.snapshot()
	if len(frames) != len(items) {
		t.Fatalf("frames written = %d, want %d", len(frames), len(items))
	}
	for i, frame := range frames {
		var got inventory.ReservationResponse
		if err := json.Unmarshal(frame, &got); err != nil {
			t.Fatalf("frame[%d] not JSON: %v\nbody=%s", i, err, frame)
		}
		if !reflect.DeepEqual(got.Reservation, items[i]) {
			t.Errorf("frame[%d] = %+v, want %+v", i, got.Reservation, items[i])
		}
	}

	unsubMu.Lock()
	defer unsubMu.Unlock()
	if unsubID != expectedSubID {
		t.Errorf("Unsubscribe got id=%q, want %q", unsubID, expectedSubID)
	}
}

// TestReservationSubscribe_StopsOnWriteError mirrors the
// disconnect-branch test on the inventory side.
func TestReservationSubscribe_StopsOnWriteError(t *testing.T) {
	mockSvc := inventory.NewMockReservationService()
	expectedSubID := inventory.ReservationsSubID("subid-disconnect")
	items := getTestReservations()

	mockSvc.SubscribeReservationsFunc = func(ch chan<- inventory.Reservation) inventory.ReservationsSubID {
		go func() {
			defer close(ch)
			for _, item := range items {
				ch <- item
			}
		}()
		return expectedSubID
	}

	var unsubCalled bool
	mockSvc.UnsubscribeReservationsFunc = func(id inventory.ReservationsSubID) {
		if id != expectedSubID {
			t.Errorf("Unsubscribe id=%q want %q", id, expectedSubID)
		}
		unsubCalled = true
	}

	w := &recordingTextWriter{err: errors.New("client disconnected")}
	inventory.StreamReservationsToClientForTest(mockSvc, w)

	if got := w.snapshot(); len(got) != 0 {
		t.Errorf("write-error path should never record a frame, got %d", len(got))
	}
	if !unsubCalled {
		t.Error("Unsubscribe must run on write-error exit (defer)")
	}
}

func TestReservationGet(t *testing.T) {
	ts, mockResSvc := setupReservationTestServer()
	defer ts.Close()

	tests := []struct {
		getReservationFunc func(ctx context.Context, ID uint64) (inventory.Reservation, error)
		ID                 string
		wantResponse       *inventory.ReservationResponse
		wantErr            *httpx.Problem
		wantStatusCode     int
	}{
		{
			getReservationFunc: func(ctx context.Context, ID uint64) (inventory.Reservation, error) {
				return getTestReservations()[0], nil
			},
			ID:             "1",
			wantResponse:   &inventory.ReservationResponse{Reservation: getTestReservations()[0]},
			wantErr:        nil,
			wantStatusCode: http.StatusOK,
		},
		{
			getReservationFunc: func(ctx context.Context, ID uint64) (inventory.Reservation, error) {
				return inventory.Reservation{}, persistence.ErrNotFound
			},
			ID:             "1",
			wantResponse:   nil,
			wantErr:        httpx.NotFoundProblem(),
			wantStatusCode: http.StatusNotFound,
		},
		{
			getReservationFunc: func(ctx context.Context, ID uint64) (inventory.Reservation, error) {
				return inventory.Reservation{}, errors.New("some unexpected error")
			},
			ID:             "1",
			wantResponse:   nil,
			wantErr:        httpx.InternalServerProblem(nil),
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		mockResSvc.GetReservationFunc = test.getReservationFunc

		url := ts.URL + "/" + test.ID
		res, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}

		if res.StatusCode != test.wantStatusCode {
			t.Errorf("status code got=%d want=%d", res.StatusCode, test.wantStatusCode)
		}

		if test.wantErr == nil {
			got := inventory.ReservationResponse{}
			testutil.Unmarshal(res, &got, t)

			if !reflect.DeepEqual(got, *test.wantResponse) {
				t.Errorf("reservation\n got=%+v\nwant=%+v", got, *test.wantResponse)
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

// TestReservationGetBadID is the regression test for ERR-001 B3.
// A non-numeric reservation id used to render 400 *and then fall
// through* to call GetReservation with ID=0. The fix added the
// missing return; this test asserts both halves: 400 status, and
// the service mock is never called.
func TestReservationGetBadID(t *testing.T) {
	ts, mockResSvc := setupReservationTestServer()
	defer ts.Close()
	mockResSvc.GetReservationFunc = func(ctx context.Context, ID uint64) (inventory.Reservation, error) {
		t.Fatalf("GetReservation should not be called for a bad ID, got ID=%d", ID)
		return inventory.Reservation{}, nil
	}

	res, err := http.Get(ts.URL + "/notanumber")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status code got=%d want=%d", res.StatusCode, http.StatusBadRequest)
	}
	if mockResSvc.GetReservationCalls != 0 {
		t.Errorf("GetReservation should not have been called, got %d calls", mockResSvc.GetReservationCalls)
	}
}

func TestReservationCreate(t *testing.T) {
	ts, mockResSvc := setupReservationTestServer()
	defer ts.Close()

	tests := []struct {
		reserveFunc    func(ctx context.Context, rr inventory.ReservationRequest) (inventory.Reservation, error)
		request        *inventory.ReservationRequestDto
		wantResponse   *inventory.ReservationResponse
		wantErr        *httpx.Problem
		wantStatusCode int
	}{
		{
			reserveFunc: func(ctx context.Context, rr inventory.ReservationRequest) (inventory.Reservation, error) {
				return getTestReservations()[0], nil
			},
			request:        createReservationRequest("requestid1", "requester1", "sku1", 1),
			wantResponse:   &inventory.ReservationResponse{Reservation: getTestReservations()[0]},
			wantErr:        nil,
			wantStatusCode: http.StatusCreated,
		},
		{
			reserveFunc: func(ctx context.Context, rr inventory.ReservationRequest) (inventory.Reservation, error) {
				return inventory.Reservation{}, persistence.ErrNotFound
			},
			request:        createReservationRequest("requestid1", "requester1", "sku1", 1),
			wantResponse:   nil,
			wantErr:        httpx.NotFoundProblem(),
			wantStatusCode: http.StatusNotFound,
		},
		{
			reserveFunc: func(ctx context.Context, rr inventory.ReservationRequest) (inventory.Reservation, error) {
				return inventory.Reservation{}, errors.New("some unexpected error")
			},
			request:        createReservationRequest("requestid1", "requester1", "sku1", 1),
			wantResponse:   nil,
			wantErr:        httpx.InternalServerProblem(nil),
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		mockResSvc.ReserveFunc = test.reserveFunc

		url := ts.URL
		res := testutil.Put(url, test.request, t)

		if res.StatusCode != test.wantStatusCode {
			t.Errorf("status code got=%d want=%d", res.StatusCode, test.wantStatusCode)
		}

		if test.wantErr == nil {
			got := inventory.ReservationResponse{}
			testutil.Unmarshal(res, &got, t)

			if !reflect.DeepEqual(got, *test.wantResponse) {
				t.Errorf("reservation\n got=%+v\nwant=%+v", got, *test.wantResponse)
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

func TestReservationList(t *testing.T) {
	ts, mockResSvc := setupReservationTestServer()
	defer ts.Close()

	tests := []struct {
		getReservationsFunc func(ctx context.Context, options inventory.GetReservationsOptions, limit int, offset int) ([]inventory.Reservation, error)
		url                 string
		wantResponse        interface{}
		wantStatusCode      int
	}{
		{
			getReservationsFunc: func(ctx context.Context, options inventory.GetReservationsOptions, limit int, offset int) ([]inventory.Reservation, error) {
				if options.Sku != "" {
					t.Errorf("sku got=%s want=%s", options.Sku, "")
				}
				if options.State != inventory.None {
					t.Errorf("state got=%s want=%s", options.State, inventory.None)
				}
				if limit != 50 {
					t.Errorf("limit got=%d want=%d", limit, 50)
				}
				if offset != 0 {
					t.Errorf("offset got=%d want=%d", offset, 0)
				}
				return getTestReservations(), nil
			},
			url:            ts.URL,
			wantResponse:   getTestReservationResponses(),
			wantStatusCode: http.StatusOK,
		},
		{
			getReservationsFunc: func(ctx context.Context, options inventory.GetReservationsOptions, limit int, offset int) ([]inventory.Reservation, error) {
				if options.Sku != "somesku" {
					t.Errorf("sku got=%s want=%s", options.Sku, "somesku")
				}
				if options.State != inventory.Open {
					t.Errorf("state got=%s want=%s", options.State, inventory.Open)
				}
				if limit != 111 {
					t.Errorf("limit got=%d want=%d", limit, 111)
				}
				if offset != 222 {
					t.Errorf("offset got=%d want=%d", offset, 0)
				}
				return getTestReservations(), nil
			},
			url:            ts.URL + "?sku=somesku&state=Open&limit=111&offset=222",
			wantResponse:   getTestReservationResponses(),
			wantStatusCode: http.StatusOK,
		},
		{
			getReservationsFunc: func(ctx context.Context, options inventory.GetReservationsOptions, limit int, offset int) ([]inventory.Reservation, error) {
				if options.State != inventory.Closed {
					t.Errorf("state got=%s want=%s", options.State, inventory.Closed)
				}
				return getTestReservations(), nil
			},
			url:            ts.URL + "?state=Closed",
			wantResponse:   getTestReservationResponses(),
			wantStatusCode: http.StatusOK,
		},
		{
			getReservationsFunc: nil,
			url:                 ts.URL + "?state=SomeInvalidState",
			wantResponse:        httpx.BadRequestProblem(errors.New("invalid state")),
			wantStatusCode:      http.StatusBadRequest,
		},
		{
			getReservationsFunc: func(ctx context.Context, options inventory.GetReservationsOptions, limit int, offset int) ([]inventory.Reservation, error) {
				return []inventory.Reservation{}, persistence.ErrNotFound
			},
			url:            ts.URL,
			wantResponse:   httpx.NotFoundProblem(),
			wantStatusCode: http.StatusNotFound,
		},
		{
			getReservationsFunc: func(ctx context.Context, options inventory.GetReservationsOptions, limit int, offset int) ([]inventory.Reservation, error) {
				return []inventory.Reservation{}, nil
			},
			url:            ts.URL + "?sku=someunknownsku",
			wantResponse:   convertReservationsToResponse([]inventory.Reservation{}),
			wantStatusCode: http.StatusOK,
		},
		{
			getReservationsFunc: func(ctx context.Context, options inventory.GetReservationsOptions, limit int, offset int) ([]inventory.Reservation, error) {
				return []inventory.Reservation{}, errors.New("some unexpected error")
			},
			url:            ts.URL,
			wantResponse:   httpx.InternalServerProblem(nil),
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		mockResSvc.GetReservationsFunc = test.getReservationsFunc

		res, err := http.Get(test.url)
		if err != nil {
			t.Fatal(err)
		}

		if res.StatusCode != test.wantStatusCode {
			t.Errorf("status code got=%d want=%d", res.StatusCode, test.wantStatusCode)
		}

		if test.wantStatusCode == http.StatusBadRequest ||
			test.wantStatusCode == http.StatusInternalServerError ||
			test.wantStatusCode == http.StatusNotFound {

			want := test.wantResponse.(*httpx.Problem)
			got := &httpx.Problem{}
			testutil.Unmarshal(res, got, t)

			if got.Title != want.Title {
				t.Errorf("status text got=%s want=%s", got.Title, want.Title)
			}
			if got.Detail != want.Detail {
				t.Errorf("error text got=%s want=%s", got.Detail, want.Detail)
			}
		} else {
			want := test.wantResponse.([]inventory.ReservationResponse)
			got := []inventory.ReservationResponse{}
			testutil.Unmarshal(res, &got, t)

			if !reflect.DeepEqual(got, want) {
				t.Errorf("reservation\n got=%+v\nwant=%+v", got, want)
			}
		}
	}
}

func createReservationRequest(requestID, requester, sku string, quantity int64) *inventory.ReservationRequestDto {
	return &inventory.ReservationRequestDto{
		ReservationRequest: &inventory.ReservationRequest{
			Sku: sku, RequestID: requestID, Requester: requester, Quantity: quantity,
		},
	}
}

func setupReservationTestServer() (*httptest.Server, *inventory.MockReservationService) {
	mockSvc := inventory.NewMockReservationService()
	invApi := inventory.NewReservationApi(mockSvc)
	r := chi.NewRouter()
	invApi.ConfigureRouter(r)
	ts := httptest.NewServer(r)

	return ts, mockSvc
}

var testReservations = []inventory.Reservation{
	{ID: 1, RequestID: "requestID1", Requester: "requester1", Sku: "sku1", State: inventory.Closed, ReservedQuantity: 1, RequestedQuantity: 1, Created: getTime("2020-01-01T01:01:01Z")},
	{ID: 2, RequestID: "requestID2", Requester: "requester2", Sku: "sku2", State: inventory.Open, ReservedQuantity: 1, RequestedQuantity: 2, Created: getTime("2020-01-01T01:01:01Z")},
	{ID: 3, RequestID: "requestID3", Requester: "requester3", Sku: "sku3", State: inventory.None, ReservedQuantity: 0, RequestedQuantity: 3, Created: getTime("2020-01-01T01:01:01Z")},
}

func getTestReservations() []inventory.Reservation {
	return testReservations
}

func getTestReservationResponses() []inventory.ReservationResponse {
	responses := []inventory.ReservationResponse{}

	for _, res := range testReservations {
		responses = append(responses, inventory.ReservationResponse{Reservation: res})
	}

	return responses
}

func convertReservationsToResponse(reservations []inventory.Reservation) []inventory.ReservationResponse {
	responses := []inventory.ReservationResponse{}

	for _, res := range reservations {
		responses = append(responses, inventory.ReservationResponse{Reservation: res})
	}

	return responses
}

func getTime(t string) time.Time {
	tm, err := time.Parse(time.RFC3339, t)
	if err != nil {
		panic(err)
	}
	return tm
}
