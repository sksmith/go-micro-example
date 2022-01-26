package inventory

import "context"

type MockInventoryService struct {
	ProduceFunc                func(ctx context.Context, product Product, event ProductionRequest) error
	CreateProductFunc          func(ctx context.Context, product Product) error
	GetProductFunc             func(ctx context.Context, sku string) (Product, error)
	GetAllProductInventoryFunc func(ctx context.Context, limit, offset int) ([]ProductInventory, error)
	GetProductInventoryFunc    func(ctx context.Context, sku string) (ProductInventory, error)
	SubscribeInventoryFunc     func(ch chan<- ProductInventory) (id InventorySubscriptionID)
	UnsubscribeInventoryFunc   func(id InventorySubscriptionID)
}

func NewMockInventoryService() MockInventoryService {
	return MockInventoryService{
		ProduceFunc:       func(ctx context.Context, product Product, event ProductionRequest) error { return nil },
		CreateProductFunc: func(ctx context.Context, product Product) error { return nil },
		GetProductFunc:    func(ctx context.Context, sku string) (Product, error) { return Product{}, nil },
		GetAllProductInventoryFunc: func(ctx context.Context, limit, offset int) ([]ProductInventory, error) {
			return []ProductInventory{}, nil
		},
		GetProductInventoryFunc:  func(ctx context.Context, sku string) (ProductInventory, error) { return ProductInventory{}, nil },
		SubscribeInventoryFunc:   func(ch chan<- ProductInventory) (id InventorySubscriptionID) { return "" },
		UnsubscribeInventoryFunc: func(id InventorySubscriptionID) {},
	}
}

func (i *MockInventoryService) Produce(ctx context.Context, product Product, event ProductionRequest) error {
	return i.ProduceFunc(ctx, product, event)
}

func (i *MockInventoryService) CreateProduct(ctx context.Context, product Product) error {
	return i.CreateProductFunc(ctx, product)
}

func (i *MockInventoryService) GetProduct(ctx context.Context, sku string) (Product, error) {
	return i.GetProductFunc(ctx, sku)
}

func (i *MockInventoryService) GetAllProductInventory(ctx context.Context, limit, offset int) ([]ProductInventory, error) {
	return i.GetAllProductInventoryFunc(ctx, limit, offset)
}

func (i *MockInventoryService) GetProductInventory(ctx context.Context, sku string) (ProductInventory, error) {
	return i.GetProductInventoryFunc(ctx, sku)
}

func (i *MockInventoryService) SubscribeInventory(ch chan<- ProductInventory) (id InventorySubscriptionID) {
	return i.SubscribeInventoryFunc(ch)
}

func (i *MockInventoryService) UnsubscribeInventory(id InventorySubscriptionID) {
	i.UnsubscribeInventoryFunc(id)
}

type MockReservationService struct {
	ReserveFunc func(ctx context.Context, rr ReservationRequest) (Reservation, error)

	GetReservationsFunc func(ctx context.Context, options GetReservationsOptions, limit, offset int) ([]Reservation, error)
	GetReservationFunc  func(ctx context.Context, ID uint64) (Reservation, error)

	SubscribeReservationsFunc   func(ch chan<- Reservation) (id ReservationsSubscriptionID)
	UnsubscribeReservationsFunc func(id ReservationsSubscriptionID)
}

func NewMockReservationService() MockReservationService {
	return MockReservationService{
		ReserveFunc: func(ctx context.Context, rr ReservationRequest) (Reservation, error) { return Reservation{}, nil },
		GetReservationsFunc: func(ctx context.Context, options GetReservationsOptions, limit, offset int) ([]Reservation, error) {
			return []Reservation{}, nil
		},
		GetReservationFunc:          func(ctx context.Context, ID uint64) (Reservation, error) { return Reservation{}, nil },
		SubscribeReservationsFunc:   func(ch chan<- Reservation) (id ReservationsSubscriptionID) { return "" },
		UnsubscribeReservationsFunc: func(id ReservationsSubscriptionID) {},
	}
}

func (r *MockReservationService) Reserve(ctx context.Context, rr ReservationRequest) (Reservation, error) {
	return r.ReserveFunc(ctx, rr)
}

func (r *MockReservationService) GetReservations(ctx context.Context, options GetReservationsOptions, limit, offset int) ([]Reservation, error) {
	return r.GetReservationsFunc(ctx, options, limit, offset)
}

func (r *MockReservationService) GetReservation(ctx context.Context, ID uint64) (Reservation, error) {
	return r.GetReservationFunc(ctx, ID)
}

func (r *MockReservationService) SubscribeReservations(ch chan<- Reservation) (id ReservationsSubscriptionID) {
	return r.SubscribeReservationsFunc(ch)
}

func (r *MockReservationService) UnsubscribeReservations(id ReservationsSubscriptionID) {
	r.UnsubscribeReservationsFunc(id)
}
