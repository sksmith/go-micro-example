package inventory_test

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/db"
	"github.com/sksmith/go-micro-example/db/invrepo"
	"github.com/sksmith/go-micro-example/queue"
	"github.com/sksmith/go-micro-example/test"
)

func TestCreateProduct(t *testing.T) {
	tests := []struct {
		name string

		product inventory.Product

		getProductFunc           func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error)
		saveProductFunc          func(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error
		saveProductInventoryFunc func(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error

		wantSaveProductCallCount     int
		wantSaveProductInventoryFunc int
		wantCommitCount              int
		wantRollbackCount            int
		wantErr                      error
	}{
		{
			name:    "new product and inventory are saved",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			getProductFunc: func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
				return inventory.Product{}, core.ErrNotFound
			},
			saveProductFunc: func(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error {
				return nil
			},
			saveProductInventoryFunc: func(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error {
				return nil
			},

			wantSaveProductCallCount:     1,
			wantSaveProductInventoryFunc: 1,
			wantCommitCount:              1,
			wantRollbackCount:            1,
			wantErr:                      nil,
		},
		{
			name:    "product already exists",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			getProductFunc: func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
				return inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"}, nil
			},
			saveProductFunc: func(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error {
				return nil
			},
			saveProductInventoryFunc: func(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error {
				return nil
			},

			wantSaveProductCallCount:     0,
			wantSaveProductInventoryFunc: 0,
			wantCommitCount:              0,
			wantRollbackCount:            0,
			wantErr:                      nil,
		},
		{
			name:    "unexpected error getting product",
			product: inventory.Product{Name: "productname", Sku: "productsku", Upc: "productupc"},

			getProductFunc: func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
				return inventory.Product{}, errors.New("some unexpected error")
			},
			saveProductFunc: func(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error {
				return nil
			},
			saveProductInventoryFunc: func(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error {
				return nil
			},

			wantSaveProductCallCount:     0,
			wantSaveProductInventoryFunc: 0,
			wantCommitCount:              0,
			wantRollbackCount:            0,
			wantErr:                      errors.New("some unexpected error"),
		},
	}

	for _, test := range tests {
		mockRepo := invrepo.NewMockRepo()
		mockRepo.GetProductFunc = test.getProductFunc
		mockRepo.SaveProductFunc = test.saveProductFunc
		mockRepo.SaveProductInventoryFunc = test.saveProductInventoryFunc

		tx := db.NewMockTransaction()
		mockRepo.BeginTransactionFunc = func(ctx context.Context) (core.Transaction, error) {
			return tx, nil
		}

		mockQueue := queue.NewMockQueue()

		service := inventory.NewService(mockRepo, mockQueue)

		t.Run(test.name, func(t *testing.T) {
			err := service.CreateProduct(context.Background(), test.product)
			if test.wantErr != nil {
				if test.wantErr.Error() != err.Error() {
					t.Errorf("unexpected error got=%v want=%v", err, test.wantErr)
				}
			} else if err != nil {
				t.Errorf("unexpected error got=%v", err)
			}

			// cnt := mockRepo.GetCallCount("SaveProduct")
			// if cnt != test.wantSaveProductCallCount {
			// 	t.Errorf("SaveProduct call count got=%d want=%d", cnt, test.wantSaveProductCallCount)
			// }

			verifyCallCount(mockRepo.CallWatcher, "SaveProduct", test.wantSaveProductCallCount, t)

			// if saveProductInventoryCallCnt != test.wantSaveProductInventoryFunc {
			// 	t.Errorf("SaveProductInventory call count got=%d want=%d", saveProductInventoryCallCnt, test.wantSaveProductCallCount)
			// }
		})
	}
}

func verifyCallCount(w *test.CallWatcher, funcName string, want int, t *testing.T) {
	cnt := w.GetCallCount(funcName)
	if cnt != want {
		t.Errorf("%s call count got=%d want=%d", funcName, cnt, want)
	}
}
