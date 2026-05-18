package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sksmith/go-micro-example/internal/inventory"
	"github.com/sksmith/go-micro-example/internal/platform/cache"
	"github.com/sksmith/go-micro-example/internal/platform/persistence"
	"github.com/sksmith/go-micro-example/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.ConfigLogging()
	os.Exit(m.Run())
}

// repoCounts is the expected call count for each inventory.MockRepo
// method exercised by the service tests. Adding a new field is the
// way to add a new assertion — typoed names won't compile.
type repoCounts struct {
	SaveProduct          int
	SaveProductInventory int
	SaveProductionEvent  int
	SaveReservation      int
}

type txCounts struct {
	Commit   int
	Rollback int
}

type queueCounts struct {
	PublishInventory   int
	PublishReservation int
}

func verifyRepoCalls(t *testing.T, m *inventory.MockRepo, want repoCounts) {
	t.Helper()
	if m.SaveProductCalls != want.SaveProduct {
		t.Errorf("SaveProduct calls got=%d want=%d", m.SaveProductCalls, want.SaveProduct)
	}
	if m.SaveProductInventoryCalls != want.SaveProductInventory {
		t.Errorf("SaveProductInventory calls got=%d want=%d", m.SaveProductInventoryCalls, want.SaveProductInventory)
	}
	if m.SaveProductionEventCalls != want.SaveProductionEvent {
		t.Errorf("SaveProductionEvent calls got=%d want=%d", m.SaveProductionEventCalls, want.SaveProductionEvent)
	}
	if m.SaveReservationCalls != want.SaveReservation {
		t.Errorf("SaveReservation calls got=%d want=%d", m.SaveReservationCalls, want.SaveReservation)
	}
}

func verifyTxCalls(t *testing.T, m *persistence.MockTransaction, want txCounts) {
	t.Helper()
	if m.CommitCalls != want.Commit {
		t.Errorf("Commit calls got=%d want=%d", m.CommitCalls, want.Commit)
	}
	if m.RollbackCalls != want.Rollback {
		t.Errorf("Rollback calls got=%d want=%d", m.RollbackCalls, want.Rollback)
	}
}

// verifySubTxCalls mirrors verifyTxCalls but for the sub-transaction
// mock used in TestFillReserves, which is a *persistence.MockPgxTx (the
// pgx.Tx-shaped mock returned from a transaction's own Begin call)
// rather than a *persistence.MockTransaction.
func verifySubTxCalls(t *testing.T, m *persistence.MockPgxTx, want txCounts) {
	t.Helper()
	if m.CommitCalls != want.Commit {
		t.Errorf("sub-tx Commit calls got=%d want=%d", m.CommitCalls, want.Commit)
	}
	if m.RollbackCalls != want.Rollback {
		t.Errorf("sub-tx Rollback calls got=%d want=%d", m.RollbackCalls, want.Rollback)
	}
}

func verifyQueueCalls(t *testing.T, m *inventory.MockQueue, want queueCounts) {
	t.Helper()
	if m.PublishInventoryCalls != want.PublishInventory {
		t.Errorf("PublishInventory calls got=%d want=%d", m.PublishInventoryCalls, want.PublishInventory)
	}
	if m.PublishReservationCalls != want.PublishReservation {
		t.Errorf("PublishReservation calls got=%d want=%d", m.PublishReservationCalls, want.PublishReservation)
	}
}

func TestCreateProduct(t *testing.T) {
	tests := []struct {
		name string

		product inventory.Product

		getProductFunc           func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.Product, error)
		saveProductFunc          func(ctx context.Context, product inventory.Product, options ...persistence.UpdateOptions) error
		saveProductInventoryFunc func(ctx context.Context, productInventory inventory.ProductInventory, options ...persistence.UpdateOptions) error

		beginTransactionFunc func(ctx context.Context) (persistence.Transaction, error)
		commitFunc           func(ctx context.Context) error

		wantRepoCalls repoCounts
		wantTxCalls   txCounts
		wantErr       bool
	}{
		{
			name:    "new product and inventory are saved",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			wantRepoCalls: repoCounts{SaveProduct: 1, SaveProductInventory: 1},
			wantTxCalls:   txCounts{Commit: 1, Rollback: 0},
			wantErr:       false,
		},
		{
			name:    "product already exists",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			getProductFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.Product, error) {
				return inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"}, nil
			},

			wantRepoCalls: repoCounts{SaveProduct: 0, SaveProductInventory: 0},
			wantTxCalls:   txCounts{Commit: 0, Rollback: 0},
			wantErr:       false,
		},
		{
			name:    "unexpected error getting product",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			getProductFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.Product, error) {
				return inventory.Product{}, errors.New("some unexpected error")
			},

			wantRepoCalls: repoCounts{SaveProduct: 0, SaveProductInventory: 0},
			wantTxCalls:   txCounts{Commit: 0, Rollback: 0},
			wantErr:       true,
		},
		{
			name:    "unexpected error saving product",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			saveProductFunc: func(ctx context.Context, product inventory.Product, options ...persistence.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantRepoCalls: repoCounts{SaveProduct: 1, SaveProductInventory: 0},
			wantTxCalls:   txCounts{Commit: 0, Rollback: 1},
			wantErr:       true,
		},
		{
			name:    "unexpected error saving product inventory",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			saveProductInventoryFunc: func(ctx context.Context, productInventory inventory.ProductInventory, options ...persistence.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantRepoCalls: repoCounts{SaveProduct: 1, SaveProductInventory: 1},
			wantTxCalls:   txCounts{Commit: 0, Rollback: 1},
			wantErr:       true,
		},
		{
			name:    "unexpected error beginning transaction",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			beginTransactionFunc: func(ctx context.Context) (persistence.Transaction, error) {
				return nil, errors.New("some unexpected error")
			},

			wantRepoCalls: repoCounts{SaveProduct: 0, SaveProductInventory: 0},
			wantTxCalls:   txCounts{Commit: 0, Rollback: 0},
			wantErr:       true,
		},
		{
			name:    "unexpected error comitting",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			commitFunc: func(ctx context.Context) error { return errors.New("some unexpected error") },

			wantRepoCalls: repoCounts{SaveProduct: 1, SaveProductInventory: 1},
			wantTxCalls:   txCounts{Commit: 1, Rollback: 1},
			wantErr:       true,
		},
	}

	for _, test := range tests {
		mockRepo := inventory.NewMockRepo()
		if test.getProductFunc != nil {
			mockRepo.GetProductFunc = test.getProductFunc
		} else {
			mockRepo.GetProductFunc = func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.Product, error) {
				return inventory.Product{}, persistence.ErrNotFound
			}
		}
		if test.saveProductFunc != nil {
			mockRepo.SaveProductFunc = test.saveProductFunc
		}
		if test.saveProductInventoryFunc != nil {
			mockRepo.SaveProductInventoryFunc = test.saveProductInventoryFunc
		}

		mockTx := persistence.NewMockTransaction()
		if test.beginTransactionFunc != nil {
			mockRepo.BeginTransactionFunc = test.beginTransactionFunc
		} else {
			mockRepo.BeginTransactionFunc = func(ctx context.Context) (persistence.Transaction, error) {
				return mockTx, nil
			}
		}

		if test.commitFunc != nil {
			mockTx.CommitFunc = test.commitFunc
		}

		mockQueue := inventory.NewMockQueue()

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			err := service.CreateProduct(context.Background(), test.product)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}
			verifyRepoCalls(t, mockRepo, test.wantRepoCalls)
			verifyTxCalls(t, mockTx, test.wantTxCalls)
		})
	}
}

func TestProduce(t *testing.T) {
	product := inventory.Product{Sku: "somesku", Upc: "someupc", Name: "somename"}
	var productInventory *inventory.ProductInventory

	tests := []struct {
		name    string
		request inventory.ProductionRequest

		getProductionEventByRequestIDFunc func(ctx context.Context, requestID string, options ...persistence.QueryOptions) (pe inventory.ProductionEvent, err error)
		saveProductionEventFunc           func(ctx context.Context, event *inventory.ProductionEvent, options ...persistence.UpdateOptions) error
		getProductInventoryFunc           func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error)
		saveProductInventoryFunc          func(ctx context.Context, productInventory inventory.ProductInventory, options ...persistence.UpdateOptions) error

		publishInventoryFunc   func(ctx context.Context, productInventory inventory.ProductInventory) error
		publishReservationFunc func(ctx context.Context, reservation inventory.Reservation) error

		beginTransactionFunc func(ctx context.Context) (persistence.Transaction, error)
		commitFunc           func(ctx context.Context) error

		wantRepoCalls  repoCounts
		wantQueueCalls queueCounts
		wantTxCalls    txCounts
		wantAvailable  int64
		wantErr        bool
	}{
		{
			name:    "inventory is incremented",
			request: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			wantRepoCalls:  repoCounts{SaveProductionEvent: 1, SaveProductInventory: 1},
			wantQueueCalls: queueCounts{PublishInventory: 1, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 2, Rollback: 0},
			wantAvailable:  2,
		},
		{
			name:    "cannot produce zero",
			request: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 0},

			wantRepoCalls:  repoCounts{SaveProductionEvent: 0, SaveProductInventory: 0},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 0},
			wantAvailable:  1,
			wantErr:        true,
		},
		{
			name:    "cannot produce negative",
			request: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: -1},

			wantRepoCalls:  repoCounts{SaveProductionEvent: 0, SaveProductInventory: 0},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 0},
			wantAvailable:  1,
			wantErr:        true,
		},
		{
			name:    "request id is required",
			request: inventory.ProductionRequest{RequestID: "", Quantity: 1},

			wantRepoCalls:  repoCounts{SaveProductionEvent: 0, SaveProductInventory: 0},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 0},
			wantAvailable:  1,
			wantErr:        true,
		},
		{
			name:    "production event already exists",
			request: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			getProductionEventByRequestIDFunc: func(ctx context.Context, requestID string, options ...persistence.QueryOptions) (pe inventory.ProductionEvent, err error) {
				return inventory.ProductionEvent{RequestID: "somerequestid", Quantity: 1}, nil
			},

			wantRepoCalls:  repoCounts{SaveProductionEvent: 0, SaveProductInventory: 0},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 0},
			wantAvailable:  1,
		},
		{
			name:    "unexpected error getting production event",
			request: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			getProductionEventByRequestIDFunc: func(ctx context.Context, requestID string, options ...persistence.QueryOptions) (pe inventory.ProductionEvent, err error) {
				return inventory.ProductionEvent{}, errors.New("some unexpected error")
			},

			wantRepoCalls:  repoCounts{SaveProductionEvent: 0, SaveProductInventory: 0},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 0},
			wantAvailable:  1,
			wantErr:        true,
		},
		{
			name:    "unexpected error beginning transaction",
			request: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			beginTransactionFunc: func(ctx context.Context) (persistence.Transaction, error) {
				return nil, errors.New("some unexpected error")
			},

			wantRepoCalls:  repoCounts{SaveProductionEvent: 0, SaveProductInventory: 0},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 0},
			wantAvailable:  1,
			wantErr:        true,
		},
		{
			name:    "unexpected error saving production event",
			request: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			saveProductionEventFunc: func(ctx context.Context, event *inventory.ProductionEvent, options ...persistence.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantRepoCalls:  repoCounts{SaveProductionEvent: 1, SaveProductInventory: 0},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 1},
			wantAvailable:  1,
			wantErr:        true,
		},
		{
			name:    "unexpected error saving product inventory",
			request: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			saveProductInventoryFunc: func(ctx context.Context, productInventory inventory.ProductInventory, options ...persistence.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantRepoCalls:  repoCounts{SaveProductionEvent: 1, SaveProductInventory: 1},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 1},
			wantAvailable:  1,
			wantErr:        true,
		},
		{
			name:    "unexpected error comitting",
			request: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			commitFunc: func(ctx context.Context) error {
				return errors.New("some unexpected error")
			},

			wantRepoCalls:  repoCounts{SaveProductionEvent: 1, SaveProductInventory: 1},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 1, Rollback: 1},
			wantAvailable:  2,
			wantErr:        true,
		},
	}

	for _, test := range tests {
		productInventory = &inventory.ProductInventory{Product: product, Available: 1}

		mockTx := persistence.NewMockTransaction()
		if test.commitFunc != nil {
			mockTx.CommitFunc = test.commitFunc
		}

		mockRepo := inventory.NewMockRepo()
		if test.beginTransactionFunc != nil {
			mockRepo.BeginTransactionFunc = test.beginTransactionFunc
		} else {
			mockRepo.BeginTransactionFunc = func(ctx context.Context) (persistence.Transaction, error) {
				return mockTx, nil
			}
		}
		if test.getProductionEventByRequestIDFunc != nil {
			mockRepo.GetProductionEventByRequestIDFunc = test.getProductionEventByRequestIDFunc
		}
		if test.saveProductionEventFunc != nil {
			mockRepo.SaveProductionEventFunc = test.saveProductionEventFunc
		}
		if test.getProductInventoryFunc != nil {
			mockRepo.GetProductInventoryFunc = test.getProductInventoryFunc
		} else {
			mockRepo.GetProductInventoryFunc = func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error) {
				return *productInventory, nil
			}
		}
		if test.saveProductInventoryFunc != nil {
			mockRepo.SaveProductInventoryFunc = test.saveProductInventoryFunc
		} else {
			mockRepo.SaveProductInventoryFunc = func(ctx context.Context, pi inventory.ProductInventory, options ...persistence.UpdateOptions) error {
				productInventory = &pi
				return nil
			}
		}

		mockQueue := inventory.NewMockQueue()
		if test.publishInventoryFunc != nil {
			mockQueue.PublishInventoryFunc = test.publishInventoryFunc
		}
		if test.publishReservationFunc != nil {
			mockQueue.PublishReservationFunc = test.publishReservationFunc
		}

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			err := service.Produce(context.Background(), product, test.request)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if productInventory.Available != test.wantAvailable {
				t.Errorf("unexpected available got=%d want=%d", productInventory.Available, test.wantAvailable)
			}
			verifyRepoCalls(t, mockRepo, test.wantRepoCalls)
			verifyQueueCalls(t, mockQueue, test.wantQueueCalls)
			verifyTxCalls(t, mockTx, test.wantTxCalls)
		})
	}
}

func TestReserve(t *testing.T) {
	tests := []struct {
		name    string
		request inventory.ReservationRequest

		getProductFunc                func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.Product, error)
		getReservationByRequestIDFunc func(ctx context.Context, requestId string, options ...persistence.QueryOptions) (inventory.Reservation, error)
		saveReservationFunc           func(ctx context.Context, reservation *inventory.Reservation, options ...persistence.UpdateOptions) error

		beginTransactionFunc func(ctx context.Context) (persistence.Transaction, error)
		commitFunc           func(ctx context.Context) error

		wantRepoCalls  repoCounts
		wantQueueCalls queueCounts
		wantTxCalls    txCounts
		wantState      inventory.ReserveState
		wantErr        bool
	}{
		{
			name:    "reservation is created",
			request: inventory.ReservationRequest{RequestID: "somerequestid", Sku: "somesku", Requester: "somerequester", Quantity: 1},

			wantRepoCalls:  repoCounts{SaveReservation: 1},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 2, Rollback: 0},
			wantState:      inventory.Open,
		},
		{
			name:          "reservation request id is required",
			request:       inventory.ReservationRequest{Sku: "somesku", Requester: "somerequester", Quantity: 1},
			wantRepoCalls: repoCounts{SaveReservation: 0},
			wantErr:       true,
		},
		{
			name:          "reservation sku is required",
			request:       inventory.ReservationRequest{RequestID: "somerequestid", Requester: "somerequester", Quantity: 1},
			wantRepoCalls: repoCounts{SaveReservation: 0},
			wantErr:       true,
		},
		{
			name:          "reservation requester is required",
			request:       inventory.ReservationRequest{RequestID: "somerequestid", Sku: "somesku", Quantity: 1},
			wantRepoCalls: repoCounts{SaveReservation: 0},
			wantErr:       true,
		},
		{
			name:          "reservation quantity must be greater than zero",
			request:       inventory.ReservationRequest{RequestID: "somerequestid", Sku: "somesku", Requester: "somerequester", Quantity: 0},
			wantRepoCalls: repoCounts{SaveReservation: 0},
			wantErr:       true,
		},
		{
			name:          "reservation quantity must not be negative",
			request:       inventory.ReservationRequest{RequestID: "somerequestid", Sku: "somesku", Requester: "somerequester", Quantity: -1},
			wantRepoCalls: repoCounts{SaveReservation: 0},
			wantErr:       true,
		},
		{
			name:    "unexpected error beginning transaction",
			request: inventory.ReservationRequest{RequestID: "somerequestid", Sku: "somesku", Requester: "somerequester", Quantity: 1},

			beginTransactionFunc: func(ctx context.Context) (persistence.Transaction, error) {
				return nil, errors.New("some unexpected error")
			},

			wantRepoCalls:  repoCounts{SaveReservation: 0},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 0},
			wantErr:        true,
		},
		{
			name:    "unexpected error getting product",
			request: inventory.ReservationRequest{RequestID: "somerequestid", Sku: "somesku", Requester: "somerequester", Quantity: 1},

			getProductFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.Product, error) {
				return inventory.Product{}, errors.New("unexpected error")
			},

			wantRepoCalls:  repoCounts{SaveReservation: 0},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 1},
			wantErr:        true,
		},
		{
			name:    "reservation request has already been processed",
			request: inventory.ReservationRequest{RequestID: "somerequestid", Sku: "somesku", Requester: "somerequester", Quantity: 1},

			getReservationByRequestIDFunc: func(ctx context.Context, requestId string, options ...persistence.QueryOptions) (inventory.Reservation, error) {
				return inventory.Reservation{RequestID: "somerequestid"}, nil
			},

			wantRepoCalls:  repoCounts{SaveReservation: 0},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 1},
			wantErr:        false,
		},
		{
			name:    "unexpected error saving reservation",
			request: inventory.ReservationRequest{RequestID: "somerequestid", Sku: "somesku", Requester: "somerequester", Quantity: 1},

			saveReservationFunc: func(ctx context.Context, reservation *inventory.Reservation, options ...persistence.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantRepoCalls:  repoCounts{SaveReservation: 1},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 1},
			wantErr:        true,
		},
		{
			name:    "unexpected error comitting",
			request: inventory.ReservationRequest{RequestID: "somerequestid", Sku: "somesku", Requester: "somerequester", Quantity: 1},

			commitFunc: func(ctx context.Context) error {
				return errors.New("some unexpected error")
			},

			wantRepoCalls:  repoCounts{SaveReservation: 1},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantTxCalls:    txCounts{Commit: 1, Rollback: 1},
			wantErr:        true,
		},
	}

	for _, test := range tests {
		mockTx := persistence.NewMockTransaction()
		if test.commitFunc != nil {
			mockTx.CommitFunc = test.commitFunc
		}

		mockRepo := inventory.NewMockRepo()
		if test.beginTransactionFunc != nil {
			mockRepo.BeginTransactionFunc = test.beginTransactionFunc
		} else {
			mockRepo.BeginTransactionFunc = func(ctx context.Context) (persistence.Transaction, error) {
				return mockTx, nil
			}
		}
		if test.getProductFunc != nil {
			mockRepo.GetProductFunc = test.getProductFunc
		}
		if test.getReservationByRequestIDFunc != nil {
			mockRepo.GetReservationByRequestIDFunc = test.getReservationByRequestIDFunc
		} else {
			mockRepo.GetReservationByRequestIDFunc = func(ctx context.Context, requestId string, options ...persistence.QueryOptions) (inventory.Reservation, error) {
				return inventory.Reservation{}, persistence.ErrNotFound
			}
		}
		if test.saveReservationFunc != nil {
			mockRepo.SaveReservationFunc = test.saveReservationFunc
		}

		mockQueue := inventory.NewMockQueue()

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			res, err := service.Reserve(context.Background(), test.request)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if res.State != test.wantState {
				t.Errorf("unexpected state got=%s want=%s", res.State, test.wantState)
			}
			verifyRepoCalls(t, mockRepo, test.wantRepoCalls)
			verifyQueueCalls(t, mockQueue, test.wantQueueCalls)
			verifyTxCalls(t, mockTx, test.wantTxCalls)
		})
	}
}

func TestGetAllProductInventory(t *testing.T) {
	productInv := getProductInventory()
	tests := []struct {
		name   string
		limit  int
		offset int

		getAllProductInventoryFunc func(ctx context.Context, limit int, offset int, options ...persistence.QueryOptions) ([]inventory.ProductInventory, error)

		wantProductInventory []inventory.ProductInventory
		wantErr              bool
	}{
		{
			name:                 "product is returned",
			wantProductInventory: productInv,
		},
		{
			name: "error is returned",
			getAllProductInventoryFunc: func(ctx context.Context, limit, offset int, options ...persistence.QueryOptions) ([]inventory.ProductInventory, error) {
				return []inventory.ProductInventory{}, errors.New("some unexpected error")
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		mockRepo := inventory.NewMockRepo()
		if test.getAllProductInventoryFunc != nil {
			mockRepo.GetAllProductInventoryFunc = test.getAllProductInventoryFunc
		} else {
			mockRepo.GetAllProductInventoryFunc = func(ctx context.Context, limit, offset int, options ...persistence.QueryOptions) ([]inventory.ProductInventory, error) {
				return productInv, nil
			}
		}
		mockQueue := inventory.NewMockQueue()

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			res, err := service.GetAllProductInventory(context.Background(), test.limit, test.offset)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if len(res) != len(test.wantProductInventory) {
				t.Errorf("unexpected product inventory got=%v want=%v", res, test.wantProductInventory)
			}
		})
	}
}

func TestGetProduct(t *testing.T) {
	productInv := getProductInventory()
	tests := []struct {
		name   string
		limit  int
		offset int

		getProductFunc func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.Product, error)

		wantProduct inventory.Product
		wantErr     bool
	}{
		{
			name:        "product is returned",
			wantProduct: productInv[0].Product,
		},
		{
			name: "error is returned",
			getProductFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.Product, error) {
				return inventory.Product{}, errors.New("some unexpected error")
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		mockRepo := inventory.NewMockRepo()
		if test.getProductFunc != nil {
			mockRepo.GetProductFunc = test.getProductFunc
		} else {
			mockRepo.GetProductFunc = func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.Product, error) {
				return productInv[0].Product, nil
			}
		}
		mockQueue := inventory.NewMockQueue()

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			res, err := service.GetProduct(context.Background(), "sku1")
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if !reflect.DeepEqual(res, test.wantProduct) {
				t.Errorf("unexpected product inventory got=%v want=%v", res, test.wantProduct)
			}
		})
	}
}

func TestGetProductInventory(t *testing.T) {
	productInv := getProductInventory()
	tests := []struct {
		name   string
		limit  int
		offset int

		getProductInventoryFunc func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error)

		wantProductInv inventory.ProductInventory
		wantErr        bool
	}{
		{
			name:           "product is returned",
			wantProductInv: productInv[0],
		},
		{
			name: "error is returned",
			getProductInventoryFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error) {
				return inventory.ProductInventory{}, errors.New("some unexpected error")
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		mockRepo := inventory.NewMockRepo()
		if test.getProductInventoryFunc != nil {
			mockRepo.GetProductInventoryFunc = test.getProductInventoryFunc
		} else {
			mockRepo.GetProductInventoryFunc = func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.ProductInventory, error) {
				return productInv[0], nil
			}
		}
		mockQueue := inventory.NewMockQueue()

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			res, err := service.GetProductInventory(context.Background(), "sku1")

			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if !reflect.DeepEqual(res, test.wantProductInv) {
				t.Errorf("unexpected product inventory got=%v want=%v", res, test.wantProductInv)
			}
		})
	}
}

func TestGetReservation(t *testing.T) {
	reservations := getReservations()
	tests := []struct {
		name string
		ID   uint64

		getReservationFunc func(ctx context.Context, ID uint64, options ...persistence.QueryOptions) (inventory.Reservation, error)

		wantReservation inventory.Reservation
		wantErr         bool
	}{
		{
			name:            "reservation is returned",
			wantReservation: reservations[0],
		},
		{
			name: "reservation is returned",
			getReservationFunc: func(ctx context.Context, ID uint64, options ...persistence.QueryOptions) (inventory.Reservation, error) {
				return inventory.Reservation{}, errors.New("some unexpected error")
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		mockRepo := inventory.NewMockRepo()
		if test.getReservationFunc != nil {
			mockRepo.GetReservationFunc = test.getReservationFunc
		} else {
			mockRepo.GetReservationFunc = func(ctx context.Context, ID uint64, options ...persistence.QueryOptions) (inventory.Reservation, error) {
				return reservations[0], nil
			}
		}
		mockQueue := inventory.NewMockQueue()

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			res, err := service.GetReservation(context.Background(), 0)

			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if !reflect.DeepEqual(res, test.wantReservation) {
				t.Errorf("unexpected reservation got=%v want=%v", res, test.wantReservation)
			}
		})
	}
}

func TestGetReservations(t *testing.T) {
	reservations := getReservations()
	tests := []struct {
		name    string
		options inventory.GetReservationsOptions
		limit   int
		offset  int

		getReservationsFunc func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit int, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error)

		wantReservations []inventory.Reservation
		wantErr          bool
	}{
		{
			name:             "reservations are returned",
			wantReservations: reservations,
		},
		{
			name: "reservation is returned",
			getReservationsFunc: func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit int, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error) {
				return []inventory.Reservation{}, errors.New("some unexpected error")
			},
			wantReservations: []inventory.Reservation{},
			wantErr:          true,
		},
	}

	for _, test := range tests {
		mockRepo := inventory.NewMockRepo()
		if test.getReservationsFunc != nil {
			mockRepo.GetReservationsFunc = test.getReservationsFunc
		} else {
			mockRepo.GetReservationsFunc = func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit int, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error) {
				return reservations, nil
			}
		}
		mockQueue := inventory.NewMockQueue()

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			res, err := service.GetReservations(context.Background(), test.options, test.limit, test.offset)

			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if !reflect.DeepEqual(res, test.wantReservations) {
				t.Errorf("unexpected reservations got=%v want=%v", res, test.wantReservations)
			}
		})
	}
}

type reservationUpdate struct {
	ID       uint64
	State    inventory.ReserveState
	Quantity int64
}

func TestFillReserves(t *testing.T) {
	product := inventory.Product{Name: "name", Sku: "sku", Upc: "upc"}
	tests := []struct {
		name                    string
		product                 inventory.Product
		saveProductInventoryErr error
		updateReservationErr    error

		getReservationsFunc      func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit int, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error)
		getProductInventoryFunc  func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error)
		saveProductInventoryFunc func(ctx context.Context, productInventory inventory.ProductInventory, options ...persistence.UpdateOptions) error
		updateReservationFunc    func(ctx context.Context, ID uint64, state inventory.ReserveState, qty int64, options ...persistence.UpdateOptions) error

		publishInventoryFunc   func(ctx context.Context, pi inventory.ProductInventory) error
		publishReservationFunc func(ctx context.Context, r inventory.Reservation) error

		beginTransactionFunc func(ctx context.Context) (persistence.Transaction, error)
		commitFunc           func(ctx context.Context) error

		wantRepoCalls        repoCounts
		wantQueueCalls       queueCounts
		wantTxCalls          txCounts
		wantSubTxCalls       txCounts
		wantProductInventory inventory.ProductInventory
		wantResUpdates       []reservationUpdate
		wantErr              bool
	}{
		{
			name:    "enough inventory to close reservation",
			product: product,

			getReservationsFunc: func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error) {
				return []inventory.Reservation{
					{ID: 0, State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 10},
				}, nil
			},
			getProductInventoryFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error) {
				return inventory.ProductInventory{Product: product, Available: 10}, nil
			},

			wantProductInventory: inventory.ProductInventory{
				Product:   product,
				Available: 0,
			},
			wantResUpdates: []reservationUpdate{
				{ID: 0, State: inventory.Closed, Quantity: 10},
			},
			wantRepoCalls:  repoCounts{SaveProductInventory: 1},
			wantQueueCalls: queueCounts{PublishInventory: 1, PublishReservation: 1},
			wantSubTxCalls: txCounts{Commit: 1, Rollback: 0},
			wantTxCalls:    txCounts{Commit: 1, Rollback: 0},
		},
		{
			name:    "not enough inventory to close reservation",
			product: product,

			getReservationsFunc: func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error) {
				return []inventory.Reservation{
					{ID: 0, State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 10},
				}, nil
			},
			getProductInventoryFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error) {
				return inventory.ProductInventory{Product: product, Available: 5}, nil
			},

			wantProductInventory: inventory.ProductInventory{
				Product:   product,
				Available: 0,
			},
			wantResUpdates: []reservationUpdate{
				{ID: 0, State: inventory.Open, Quantity: 5},
			},

			wantRepoCalls:  repoCounts{SaveProductInventory: 1},
			wantQueueCalls: queueCounts{PublishInventory: 1, PublishReservation: 1},
			wantSubTxCalls: txCounts{Commit: 1, Rollback: 0},
			wantTxCalls:    txCounts{Commit: 1, Rollback: 0},
		},
		{
			name:    "enough inventory to close multiple reservations",
			product: product,

			getReservationsFunc: func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error) {
				return []inventory.Reservation{
					{ID: 0, State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 3},
					{ID: 1, State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 3},
					{ID: 2, State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 3},
				}, nil
			},
			getProductInventoryFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error) {
				return inventory.ProductInventory{Product: product, Available: 10}, nil
			},

			wantProductInventory: inventory.ProductInventory{
				Product:   product,
				Available: 1,
			},
			wantResUpdates: []reservationUpdate{
				{ID: 0, State: inventory.Closed, Quantity: 3},
				{ID: 1, State: inventory.Closed, Quantity: 3},
				{ID: 2, State: inventory.Closed, Quantity: 3},
			},

			wantRepoCalls:  repoCounts{SaveProductInventory: 3},
			wantQueueCalls: queueCounts{PublishInventory: 3, PublishReservation: 3},
			wantSubTxCalls: txCounts{Commit: 3, Rollback: 0},
			wantTxCalls:    txCounts{Commit: 1, Rollback: 0},
		},
		{
			name:    "unexpected error saving inventory",
			product: product,

			getReservationsFunc: func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error) {
				return []inventory.Reservation{
					{ID: 0, State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 3},
				}, nil
			},
			getProductInventoryFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error) {
				return inventory.ProductInventory{Product: product, Available: 10}, nil
			},
			saveProductInventoryErr: errors.New("some unexpected error"),

			wantErr: true,
			wantProductInventory: inventory.ProductInventory{
				Product:   product,
				Available: 7,
			},
			wantResUpdates: []reservationUpdate{},
			wantRepoCalls:  repoCounts{SaveProductInventory: 1},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantSubTxCalls: txCounts{Commit: 0, Rollback: 1},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 1},
		},
		{
			name:    "unexpected error updating reservation",
			product: product,

			getReservationsFunc: func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error) {
				return []inventory.Reservation{
					{ID: 0, State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 3},
				}, nil
			},
			getProductInventoryFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error) {
				return inventory.ProductInventory{Product: product, Available: 10}, nil
			},
			updateReservationErr: errors.New("some unexpected error"),

			wantErr: true,
			wantProductInventory: inventory.ProductInventory{
				Product:   product,
				Available: 7,
			},
			wantResUpdates: []reservationUpdate{
				{ID: 0, State: inventory.Closed, Quantity: 3},
			},
			wantRepoCalls:  repoCounts{SaveProductInventory: 1},
			wantQueueCalls: queueCounts{PublishInventory: 0, PublishReservation: 0},
			wantSubTxCalls: txCounts{Commit: 0, Rollback: 1},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 1},
		},
		{
			name:    "unexpected error publishing inventory",
			product: product,

			getReservationsFunc: func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error) {
				return []inventory.Reservation{
					{ID: 0, State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 3},
				}, nil
			},
			getProductInventoryFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error) {
				return inventory.ProductInventory{Product: product, Available: 10}, nil
			},
			publishInventoryFunc: func(ctx context.Context, pi inventory.ProductInventory) error {
				return errors.New("some unexpected error")
			},

			wantErr: true,
			wantProductInventory: inventory.ProductInventory{
				Product:   product,
				Available: 7,
			},
			wantResUpdates: []reservationUpdate{
				{ID: 0, State: inventory.Closed, Quantity: 3},
			},
			wantRepoCalls:  repoCounts{SaveProductInventory: 1},
			wantQueueCalls: queueCounts{PublishInventory: 1, PublishReservation: 0},
			wantSubTxCalls: txCounts{Commit: 1, Rollback: 1},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 1},
		},
		{
			name:    "unexpected error publishing reservation",
			product: product,

			getReservationsFunc: func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error) {
				return []inventory.Reservation{
					{ID: 0, State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 3},
				}, nil
			},
			getProductInventoryFunc: func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error) {
				return inventory.ProductInventory{Product: product, Available: 10}, nil
			},
			publishReservationFunc: func(ctx context.Context, r inventory.Reservation) error {
				return errors.New("some unexpected error")
			},

			wantErr: true,
			wantProductInventory: inventory.ProductInventory{
				Product:   product,
				Available: 7,
			},
			wantResUpdates: []reservationUpdate{
				{ID: 0, State: inventory.Closed, Quantity: 3},
			},
			wantRepoCalls:  repoCounts{SaveProductInventory: 1},
			wantQueueCalls: queueCounts{PublishInventory: 1, PublishReservation: 1},
			wantSubTxCalls: txCounts{Commit: 1, Rollback: 1},
			wantTxCalls:    txCounts{Commit: 0, Rollback: 1},
		},
	}

	for _, test := range tests {
		if test.name == "unexpected error publishing reservation" {
			fmt.Println("ugh")
		}
		mockTx := persistence.NewMockTransaction()
		if test.commitFunc != nil {
			mockTx.CommitFunc = test.commitFunc
		}

		mockSubTx := persistence.NewMockPgxTx()
		mockTx.BeginFunc = func(ctx context.Context) (pgx.Tx, error) {
			return mockSubTx, nil
		}

		mockRepo := inventory.NewMockRepo()
		if test.beginTransactionFunc != nil {
			mockRepo.BeginTransactionFunc = test.beginTransactionFunc
		} else {
			mockRepo.BeginTransactionFunc = func(ctx context.Context) (persistence.Transaction, error) {
				return mockTx, nil
			}
		}
		if test.getReservationsFunc != nil {
			mockRepo.GetReservationsFunc = test.getReservationsFunc
		}
		if test.getProductInventoryFunc != nil {
			mockRepo.GetProductInventoryFunc = test.getProductInventoryFunc
		}
		var gotProductInventory inventory.ProductInventory
		mockRepo.SaveProductInventoryFunc = func(ctx context.Context, productInventory inventory.ProductInventory, options ...persistence.UpdateOptions) error {
			gotProductInventory = productInventory
			return test.saveProductInventoryErr
		}

		gotResUpdates := []reservationUpdate{}
		mockRepo.UpdateReservationFunc = func(ctx context.Context, ID uint64, state inventory.ReserveState, qty int64, options ...persistence.UpdateOptions) error {
			gotResUpdates = append(gotResUpdates, reservationUpdate{ID: ID, State: state, Quantity: qty})
			return test.updateReservationErr
		}

		mockQueue := inventory.NewMockQueue()
		if test.publishInventoryFunc != nil {
			mockQueue.PublishInventoryFunc = test.publishInventoryFunc
		}
		if test.publishReservationFunc != nil {
			mockQueue.PublishReservationFunc = test.publishReservationFunc
		}

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			err := service.FillReserves(context.Background(), test.product)

			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if !reflect.DeepEqual(gotProductInventory, test.wantProductInventory) {
				t.Errorf("unexpected product inventory\n got=%+v\nwant=%+v", gotProductInventory, test.wantProductInventory)
			}

			if !reflect.DeepEqual(gotResUpdates, test.wantResUpdates) {
				t.Errorf("unexpected reservation updates\n got=%+v\nwant=%+v", gotResUpdates, test.wantResUpdates)
			}
			verifyRepoCalls(t, mockRepo, test.wantRepoCalls)
			verifyQueueCalls(t, mockQueue, test.wantQueueCalls)
			verifyTxCalls(t, mockTx, test.wantTxCalls)
			verifySubTxCalls(t, mockSubTx, test.wantSubTxCalls)
		})
	}
}

func TestSubscribeInventory(t *testing.T) {
	mockRepo := inventory.NewMockRepo()
	mockQueue := inventory.NewMockQueue()
	service := inventory.NewService(mockRepo, mockQueue)

	mockRepo.GetProductInventoryFunc = func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.ProductInventory, error) {
		return getProductInventory()[2], nil
	}

	ch := make(chan inventory.ProductInventory)
	id := service.SubscribeInventory(ch)

	go func() {
		_ = service.Produce(context.Background(), getProductInventory()[2].Product, inventory.ProductionRequest{RequestID: "request1", Quantity: 1})
	}()

	want := getProductInventory()[2]
	want.Available++

	select {
	case got := <-ch:
		if !reflect.DeepEqual(got, want) {
			t.Errorf("unexpected product got=%v want=%v", got, want)
		}
	case <-time.After(10 * time.Millisecond):
		t.Error("timed out waiting for product inventory from channel")
	}

	service.UnsubscribeInventory(id)

	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("channel should be closed")
		}
	case <-time.After(10 * time.Millisecond):
		t.Error("channel should be closed by now")
	}
}

func TestSubscribeReservations(t *testing.T) {
	mockRepo := inventory.NewMockRepo()
	mockQueue := inventory.NewMockQueue()
	service := inventory.NewService(mockRepo, mockQueue)

	mockRepo.GetReservationsFunc = func(ctx context.Context, resOptions inventory.GetReservationsOptions, limit int, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error) {
		return []inventory.Reservation{getReservations()[3]}, nil
	}
	mockRepo.GetProductInventoryFunc = func(ctx context.Context, sku string, options ...persistence.QueryOptions) (pi inventory.ProductInventory, err error) {
		pi = getProductInventory()[2]
		pi.Available += 10
		return pi, nil
	}

	ch := make(chan inventory.Reservation)
	id := service.SubscribeReservations(ch)

	go func() {
		_ = service.Produce(context.Background(), getProductInventory()[2].Product, inventory.ProductionRequest{RequestID: "request1", Quantity: 10})
	}()

	want := getReservations()[3]
	want.State = inventory.Closed
	want.ReservedQuantity = want.RequestedQuantity

	select {
	case got := <-ch:
		if !reflect.DeepEqual(got, want) {
			t.Errorf("unexpected reservation\n got=%+v\nwant=%+v", got, want)
		}
	case <-time.After(10 * time.Millisecond):
		t.Error("timed out waiting for reservation from channel")
	}

	service.UnsubscribeReservations(id)

	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("channel should be closed")
		}
	case <-time.After(10 * time.Millisecond):
		t.Error("channel should be closed by now")
	}
}

func getProductInventory() []inventory.ProductInventory {
	return []inventory.ProductInventory{
		{Product: inventory.Product{Sku: "sku1", Upc: "upc1", Name: "name1"}, Available: 1},
		{Product: inventory.Product{Sku: "sku2", Upc: "upc2", Name: "name2"}, Available: 10},
		{Product: inventory.Product{Sku: "sku3", Upc: "upc3", Name: "name3"}, Available: 0},
	}
}

func getReservations() []inventory.Reservation {
	return []inventory.Reservation{
		{ID: 0, RequestID: "request1", Requester: "requester1", Sku: "sku1", State: inventory.Closed, ReservedQuantity: 10, RequestedQuantity: 10},
		{ID: 1, RequestID: "request2", Requester: "requester2", Sku: "sku1", State: inventory.Closed, ReservedQuantity: 3, RequestedQuantity: 3},
		{ID: 2, RequestID: "request3", Requester: "requester1", Sku: "sku2", State: inventory.Closed, ReservedQuantity: 10, RequestedQuantity: 10},
		{ID: 3, RequestID: "request4", Requester: "requester1", Sku: "sku3", State: inventory.Open, ReservedQuantity: 2, RequestedQuantity: 10},
	}
}

// TestGetProductInventoryCacheHitSkipsRepo covers the DSN-020
// cache-aside read: a populated cache means the repository is never
// touched.
func TestGetProductInventoryCacheHitSkipsRepo(t *testing.T) {
	pi := inventory.ProductInventory{Available: 5, Product: inventory.Product{Sku: "sku1", Upc: "upc", Name: "n"}}

	mockRepo := inventory.NewMockRepo()
	var repoCalls int
	mockRepo.GetProductInventoryFunc = func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.ProductInventory, error) {
		repoCalls++
		return pi, nil
	}
	mq := inventory.NewMockQueue()
	svc := inventory.NewService(mockRepo, mq)

	c := cache.NewMemoryCache()
	svc.SetCache(c, time.Minute)
	// Prime the cache so the first call to the service sees a hit.
	if err := cache.Set(context.Background(), c, "inv:product:sku1:v1", pi, time.Minute); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	got, err := svc.GetProductInventory(context.Background(), "sku1")
	if err != nil {
		t.Fatalf("GetProductInventory: %v", err)
	}
	if got.Available != pi.Available || got.Sku != pi.Sku {
		t.Errorf("got=%+v want=%+v", got, pi)
	}
	if repoCalls != 0 {
		t.Errorf("repo called %d times on cache hit; want 0", repoCalls)
	}
}

// TestGetProductInventoryCacheMissPopulatesCache covers the
// cache-fill side of cache-aside: a miss falls through to the
// repository, then the result is written back so the next read hits.
func TestGetProductInventoryCacheMissPopulatesCache(t *testing.T) {
	pi := inventory.ProductInventory{Available: 7, Product: inventory.Product{Sku: "sku2", Upc: "upc", Name: "n"}}

	mockRepo := inventory.NewMockRepo()
	mockRepo.GetProductInventoryFunc = func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.ProductInventory, error) {
		return pi, nil
	}
	svc := inventory.NewService(mockRepo, inventory.NewMockQueue())

	c := cache.NewMemoryCache()
	svc.SetCache(c, time.Minute)

	if _, err := svc.GetProductInventory(context.Background(), "sku2"); err != nil {
		t.Fatalf("GetProductInventory: %v", err)
	}

	if c.Size() != 1 {
		t.Errorf("cache size=%d after miss; want 1 (cache should have been populated)", c.Size())
	}
	got, ok, err := cache.Get[inventory.ProductInventory](context.Background(), c, "inv:product:sku2:v1")
	if err != nil || !ok {
		t.Fatalf("cache Get: ok=%v err=%v", ok, err)
	}
	if got.Available != pi.Available {
		t.Errorf("cached available=%d want=%d", got.Available, pi.Available)
	}
}

// TestProduceInvalidatesCache covers the write-side of the DSN-020
// contract: a successful Produce reaches publishInventory after the
// transaction commits, which is where the cache key is dropped.
func TestProduceInvalidatesCache(t *testing.T) {
	pi := inventory.ProductInventory{Available: 1, Product: inventory.Product{Sku: "sku3", Upc: "upc", Name: "n"}}
	mockRepo := inventory.NewMockRepo()
	mockRepo.GetProductionEventByRequestIDFunc = func(ctx context.Context, requestID string, options ...persistence.QueryOptions) (inventory.ProductionEvent, error) {
		return inventory.ProductionEvent{}, persistence.ErrNotFound
	}
	mockRepo.GetProductInventoryFunc = func(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.ProductInventory, error) {
		return pi, nil
	}
	mockRepo.GetReservationsFunc = func(ctx context.Context, options inventory.GetReservationsOptions, limit, offset int, queryOptions ...persistence.QueryOptions) ([]inventory.Reservation, error) {
		return nil, nil
	}

	svc := inventory.NewService(mockRepo, inventory.NewMockQueue())
	c := cache.NewMemoryCache()
	svc.SetCache(c, time.Minute)

	// Plant a cached entry so we can confirm Produce dropped it.
	_ = cache.Set(context.Background(), c, "inv:product:sku3:v1", pi, time.Minute)
	if c.Size() != 1 {
		t.Fatalf("setup: size=%d, want 1", c.Size())
	}

	err := svc.Produce(context.Background(), pi.Product, inventory.ProductionRequest{RequestID: "r-1", Quantity: 2})
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	if c.Size() != 0 {
		t.Errorf("cache size=%d after Produce; want 0 (invalidation should have dropped sku3)", c.Size())
	}
}
