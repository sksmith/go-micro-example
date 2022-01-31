package inventory_test

import (
	"context"
	"os"
	"testing"

	"github.com/pkg/errors"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/db"
	"github.com/sksmith/go-micro-example/db/invrepo"
	"github.com/sksmith/go-micro-example/queue"
	"github.com/sksmith/go-micro-example/test"
)

func TestMain(m *testing.M) {
	test.ConfigLogging()
	os.Exit(m.Run())
}

func TestCreateProduct(t *testing.T) {
	tests := []struct {
		name string

		product inventory.Product

		getProductFunc           func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error)
		saveProductFunc          func(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error
		saveProductInventoryFunc func(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error

		wantRepoCallCnt map[string]int
		wantTxCallCnt   map[string]int
		wantErr         bool
	}{
		{
			name:    "new product and inventory are saved",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			wantRepoCallCnt: map[string]int{"SaveProduct": 1, "SaveProductInventory": 1},
			wantTxCallCnt:   map[string]int{"Commit": 1, "Rollback": 0},
			wantErr:         false,
		},
		{
			name:    "product already exists",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			getProductFunc: func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
				return inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"}, nil
			},

			wantRepoCallCnt: map[string]int{"SaveProduct": 0, "SaveProductInventory": 0},
			wantTxCallCnt:   map[string]int{"Commit": 0, "Rollback": 0},
			wantErr:         false,
		},
		{
			name:    "unexpected error getting product",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			getProductFunc: func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
				return inventory.Product{}, errors.New("some unexpected error")
			},

			wantRepoCallCnt: map[string]int{"SaveProduct": 0, "SaveProductInventory": 0},
			wantTxCallCnt:   map[string]int{"Commit": 0, "Rollback": 0},
			wantErr:         true,
		},
		{
			name:    "unexpected error saving product",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			saveProductFunc: func(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantRepoCallCnt: map[string]int{"SaveProduct": 1, "SaveProductInventory": 0},
			wantTxCallCnt:   map[string]int{"Commit": 0, "Rollback": 1},
			wantErr:         true,
		},
		{
			name:    "unexpected error saving product inventory",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			saveProductInventoryFunc: func(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantRepoCallCnt: map[string]int{"SaveProduct": 1, "SaveProductInventory": 1},
			wantTxCallCnt:   map[string]int{"Commit": 0, "Rollback": 1},
			wantErr:         true,
		},
	}

	for _, test := range tests {
		mockRepo := invrepo.NewMockRepo()
		if test.getProductFunc != nil {
			mockRepo.GetProductFunc = test.getProductFunc
		} else {
			mockRepo.GetProductFunc = func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
				return inventory.Product{}, core.ErrNotFound
			}
		}
		if test.saveProductFunc != nil {
			mockRepo.SaveProductFunc = test.saveProductFunc
		}
		if test.saveProductInventoryFunc != nil {
			mockRepo.SaveProductInventoryFunc = test.saveProductInventoryFunc
		}

		mockTx := db.NewMockTransaction()
		mockRepo.BeginTransactionFunc = func(ctx context.Context) (core.Transaction, error) {
			return mockTx, nil
		}

		mockQueue := queue.NewMockQueue()

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			err := service.CreateProduct(context.Background(), test.product)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			for f, c := range test.wantRepoCallCnt {
				mockRepo.VerifyCount(f, c, t)
			}
			for f, c := range test.wantTxCallCnt {
				mockTx.VerifyCount(f, c, t)
			}
		})
	}
}

func TestProduce(t *testing.T) {
	product := inventory.Product{Sku: "somesku", Upc: "someupc", Name: "somename"}
	var productInventory *inventory.ProductInventory

	tests := []struct {
		name string

		productionRequest inventory.ProductionRequest

		getProductionEventByRequestIDFunc func(ctx context.Context, requestID string, options ...core.QueryOptions) (pe inventory.ProductionEvent, err error)
		saveProductionEventFunc           func(ctx context.Context, event *inventory.ProductionEvent, options ...core.UpdateOptions) error
		getProductInventoryFunc           func(ctx context.Context, sku string, options ...core.QueryOptions) (pi inventory.ProductInventory, err error)
		saveProductInventoryFunc          func(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error

		publishInventoryFunc   func(ctx context.Context, productInventory inventory.ProductInventory) error
		publishReservationFunc func(ctx context.Context, reservation inventory.Reservation) error

		beginTransactionFunc func(ctx context.Context) (core.Transaction, error)
		commitFunc           func(ctx context.Context) error

		wantRepoCallCnt  map[string]int
		wantQueueCallCnt map[string]int
		wantTxCallCnt    map[string]int
		wantAvailable    int64
		wantErr          bool
	}{
		{
			name:              "inventory is incremented",
			productionRequest: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			wantRepoCallCnt:  map[string]int{"SaveProductionEvent": 1, "SaveProductInventory": 1},
			wantQueueCallCnt: map[string]int{"PublishInventory": 1, "PublishReservation": 0},
			wantTxCallCnt:    map[string]int{"Commit": 2, "Rollback": 0},
			wantAvailable:    2,
		},
		{
			name:              "cannot produce zero",
			productionRequest: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 0},

			wantRepoCallCnt:  map[string]int{"SaveProductionEvent": 0, "SaveProductInventory": 0},
			wantQueueCallCnt: map[string]int{"PublishInventory": 0, "PublishReservation": 0},
			wantTxCallCnt:    map[string]int{"Commit": 0, "Rollback": 0},
			wantAvailable:    1,
			wantErr:          true,
		},
		{
			name:              "cannot produce negative",
			productionRequest: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: -1},

			wantRepoCallCnt:  map[string]int{"SaveProductionEvent": 0, "SaveProductInventory": 0},
			wantQueueCallCnt: map[string]int{"PublishInventory": 0, "PublishReservation": 0},
			wantTxCallCnt:    map[string]int{"Commit": 0, "Rollback": 0},
			wantAvailable:    1,
			wantErr:          true,
		},
		{
			name:              "request id is required",
			productionRequest: inventory.ProductionRequest{RequestID: "", Quantity: 1},

			wantRepoCallCnt:  map[string]int{"SaveProductionEvent": 0, "SaveProductInventory": 0},
			wantQueueCallCnt: map[string]int{"PublishInventory": 0, "PublishReservation": 0},
			wantTxCallCnt:    map[string]int{"Commit": 0, "Rollback": 0},
			wantAvailable:    1,
			wantErr:          true,
		},
		{
			name:              "production event already exists",
			productionRequest: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			getProductionEventByRequestIDFunc: func(ctx context.Context, requestID string, options ...core.QueryOptions) (pe inventory.ProductionEvent, err error) {
				return inventory.ProductionEvent{RequestID: "somerequestid", Quantity: 1}, nil
			},

			wantRepoCallCnt:  map[string]int{"SaveProductionEvent": 0, "SaveProductInventory": 0},
			wantQueueCallCnt: map[string]int{"PublishInventory": 0, "PublishReservation": 0},
			wantTxCallCnt:    map[string]int{"Commit": 0, "Rollback": 0},
			wantAvailable:    1,
		},
		{
			name:              "unexpected error getting production event",
			productionRequest: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			getProductionEventByRequestIDFunc: func(ctx context.Context, requestID string, options ...core.QueryOptions) (pe inventory.ProductionEvent, err error) {
				return inventory.ProductionEvent{}, errors.New("some unexpected error")
			},

			wantRepoCallCnt:  map[string]int{"SaveProductionEvent": 0, "SaveProductInventory": 0},
			wantQueueCallCnt: map[string]int{"PublishInventory": 0, "PublishReservation": 0},
			wantTxCallCnt:    map[string]int{"Commit": 0, "Rollback": 0},
			wantAvailable:    1,
			wantErr:          true,
		},
		{
			name:              "unexpected error beginning transaction",
			productionRequest: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			beginTransactionFunc: func(ctx context.Context) (core.Transaction, error) {
				return nil, errors.New("some unexpected error")
			},

			wantRepoCallCnt:  map[string]int{"SaveProductionEvent": 0, "SaveProductInventory": 0},
			wantQueueCallCnt: map[string]int{"PublishInventory": 0, "PublishReservation": 0},
			wantTxCallCnt:    map[string]int{"Commit": 0, "Rollback": 0},
			wantAvailable:    1,
			wantErr:          true,
		},
		{
			name:              "unexpected error saving production event",
			productionRequest: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			saveProductionEventFunc: func(ctx context.Context, event *inventory.ProductionEvent, options ...core.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantRepoCallCnt:  map[string]int{"SaveProductionEvent": 1, "SaveProductInventory": 0},
			wantQueueCallCnt: map[string]int{"PublishInventory": 0, "PublishReservation": 0},
			wantTxCallCnt:    map[string]int{"Commit": 0, "Rollback": 1},
			wantAvailable:    1,
			wantErr:          true,
		},
		{
			name:              "unexpected error saving product inventory",
			productionRequest: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			saveProductInventoryFunc: func(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error {
				return errors.New("some unexpected error")
			},

			wantRepoCallCnt:  map[string]int{"SaveProductionEvent": 1, "SaveProductInventory": 1},
			wantQueueCallCnt: map[string]int{"PublishInventory": 0, "PublishReservation": 0},
			wantTxCallCnt:    map[string]int{"Commit": 0, "Rollback": 1},
			wantAvailable:    1,
			wantErr:          true,
		},
		{
			name:              "unexpected error comitting",
			productionRequest: inventory.ProductionRequest{RequestID: "somerequestid", Quantity: 1},

			commitFunc: func(ctx context.Context) error {
				return errors.New("some unexpected error")
			},

			wantRepoCallCnt:  map[string]int{"SaveProductionEvent": 1, "SaveProductInventory": 1},
			wantQueueCallCnt: map[string]int{"PublishInventory": 0, "PublishReservation": 0},
			wantTxCallCnt:    map[string]int{"Commit": 1, "Rollback": 1},
			wantAvailable:    2,
			wantErr:          true,
		},
	}

	for _, test := range tests {
		productInventory = &inventory.ProductInventory{Product: product, Available: 1}

		mockTx := db.NewMockTransaction()
		if test.commitFunc != nil {
			mockTx.CommitFunc = test.commitFunc
		}

		mockRepo := invrepo.NewMockRepo()
		if test.beginTransactionFunc != nil {
			mockRepo.BeginTransactionFunc = test.beginTransactionFunc
		} else {
			mockRepo.BeginTransactionFunc = func(ctx context.Context) (core.Transaction, error) {
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
			mockRepo.GetProductInventoryFunc = func(ctx context.Context, sku string, options ...core.QueryOptions) (pi inventory.ProductInventory, err error) {
				return *productInventory, nil
			}
		}
		if test.saveProductInventoryFunc != nil {
			mockRepo.SaveProductInventoryFunc = test.saveProductInventoryFunc
		} else {
			mockRepo.SaveProductInventoryFunc = func(ctx context.Context, pi inventory.ProductInventory, options ...core.UpdateOptions) error {
				productInventory = &pi
				return nil
			}
		}

		mockQueue := queue.NewMockQueue()
		if test.publishInventoryFunc != nil {
			mockQueue.PublishInventoryFunc = test.publishInventoryFunc
		}
		if test.publishReservationFunc != nil {
			mockQueue.PublishReservationFunc = test.publishReservationFunc
		}

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			err := service.Produce(context.Background(), product, test.productionRequest)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			} else if !test.wantErr && err != nil {
				t.Errorf("did not want error, got=%v", err)
			}

			if productInventory.Available != test.wantAvailable {
				t.Errorf("unexpected available got=%d want=%d", productInventory.Available, test.wantAvailable)
			}

			for f, c := range test.wantRepoCallCnt {
				mockRepo.VerifyCount(f, c, t)
			}
			for f, c := range test.wantQueueCallCnt {
				mockQueue.VerifyCount(f, c, t)
			}
			for f, c := range test.wantTxCallCnt {
				mockTx.VerifyCount(f, c, t)
			}
		})
	}
}
