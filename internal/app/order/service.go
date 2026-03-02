// Package order contains the application service for order management.
package order

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/event"
	"github.com/izzam/mini-exchange/internal/domain/repository"
	"github.com/izzam/mini-exchange/pkg/validator"
)

// OrderSubmitter is the interface satisfied by the matching engine (or any
// router that forwards orders to the correct engine in a partitioned cluster).
// Using an interface here decouples the service from *engine.Engine so the
// caller can inject a PartitionedRouter without changing the service.
type OrderSubmitter interface {
	SubmitOrder(order *entity.Order) error
}

// Service orchestrates order creation and querying.
type Service struct {
	orderRepo repository.OrderRepository
	engine    OrderSubmitter
	bus       event.Bus
}

// NewService constructs an order Service.
func NewService(orderRepo repository.OrderRepository, eng OrderSubmitter, bus event.Bus) *Service {
	return &Service{
		orderRepo: orderRepo,
		engine:    eng,
		bus:       bus,
	}
}

// CreateOrderRequest is the validated input for creating an order.
// It mirrors validator.CreateOrderRequest but lives here so the service
// layer owns its own input type.
type CreateOrderRequest = validator.CreateOrderRequest

// CreateOrder validates the request, persists a new order, submits it to the
// matching engine, and publishes an OrderCreated event.
func (s *Service) CreateOrder(ctx context.Context, userID string, req CreateOrderRequest) (*entity.Order, error) {
	now := time.Now()
	order := &entity.Order{
		ID:        uuid.NewString(),
		UserID:    userID,
		StockCode: req.StockCode,
		Side:      req.Side,
		Price:     req.Price,
		Quantity:  req.Quantity,
		Status:    entity.OrderStatusOpen,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.orderRepo.Save(ctx, order); err != nil {
		return nil, err
	}

	// Take the snapshot BEFORE handing the pointer to the engine.
	// Once SubmitOrder returns the engine goroutine owns the pointer and may
	// call order.Fill() concurrently; reading from it here would be a race.
	snapshot := *order

	if err := s.engine.SubmitOrder(order); err != nil {
		// Backpressure — order is saved; engine channel is full.
		// In production a retry / dead-letter queue would be appropriate.
		return &snapshot, nil
	}

	s.bus.Publish(event.Event{
		Type:      event.OrderCreated,
		StockCode: order.StockCode,
		UserID:    order.UserID,
		Payload:   order,
		Timestamp: now,
	})

	return &snapshot, nil
}

// GetOrders returns a filtered, paginated list of orders.
func (s *Service) GetOrders(ctx context.Context, filter entity.OrderFilter) ([]*entity.Order, int64, error) {
	return s.orderRepo.FindAll(ctx, filter)
}

// GetOrderByID returns a single order by ID, or repository.ErrNotFound.
func (s *Service) GetOrderByID(ctx context.Context, id string) (*entity.Order, error) {
	return s.orderRepo.FindByID(ctx, id)
}
