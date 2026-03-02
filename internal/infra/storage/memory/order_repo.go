// Package memory provides in-memory implementations of the domain repository interfaces.
// These implementations are thread-safe and suitable for single-node deployments
// and testing. For multi-node deployments, replace with PostgreSQL + Redis variants.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

// OrderRepo is a thread-safe in-memory implementation of repository.OrderRepository.
// It maintains two views of the data:
//   - byID: O(1) lookup by order ID
//   - ordered: insertion-order slice for stable pagination
type OrderRepo struct {
	mu      sync.RWMutex
	byID    map[string]*entity.Order
	ordered []*entity.Order
}

// NewOrderRepo constructs an empty OrderRepo.
func NewOrderRepo() *OrderRepo {
	return &OrderRepo{
		byID:    make(map[string]*entity.Order),
		ordered: make([]*entity.Order, 0, 256),
	}
}

// Save inserts a new order. Returns repository.ErrNotFound if the ID already exists.
func (r *OrderRepo) Save(_ context.Context, order *entity.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byID[order.ID]; exists {
		return repository.ErrDuplicate
	}

	// Store a shallow copy so callers cannot mutate stored state directly.
	cp := copyOrder(order)
	r.byID[cp.ID] = cp
	r.ordered = append(r.ordered, cp)
	return nil
}

// FindByID returns the order with the given ID, or repository.ErrNotFound.
func (r *OrderRepo) FindByID(_ context.Context, id string) (*entity.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	o, ok := r.byID[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	cp := copyOrder(o)
	return cp, nil
}

// FindAll returns orders matching the filter. The second return value is the
// total matching count before pagination is applied.
func (r *OrderRepo) FindAll(_ context.Context, filter entity.OrderFilter) ([]*entity.Order, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matched []*entity.Order
	for _, o := range r.ordered {
		if filter.StockCode != "" && o.StockCode != filter.StockCode {
			continue
		}
		if filter.Status != "" && o.Status != filter.Status {
			continue
		}
		if filter.UserID != "" && o.UserID != filter.UserID {
			continue
		}
		matched = append(matched, o)
	}

	total := int64(len(matched))

	// Apply pagination.
	page := filter.Page
	perPage := filter.PerPage
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 20
	}

	start := (page - 1) * perPage
	if start >= len(matched) {
		return []*entity.Order{}, total, nil
	}
	end := start + perPage
	if end > len(matched) {
		end = len(matched)
	}

	// Return copies so callers cannot mutate stored state.
	result := make([]*entity.Order, end-start)
	for i, o := range matched[start:end] {
		result[i] = copyOrder(o)
	}
	return result, total, nil
}

// Update overwrites the mutable fields of an existing order.
// Returns repository.ErrNotFound if no order with that ID exists.
func (r *OrderRepo) Update(_ context.Context, order *entity.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.byID[order.ID]
	if !ok {
		return repository.ErrNotFound
	}

	// Update only the mutable fields; preserve immutable identity fields.
	existing.Status = order.Status
	existing.FilledQuantity = order.FilledQuantity
	existing.UpdatedAt = time.Now()
	return nil
}

// copyOrder returns a shallow copy of an order (fields are value types).
func copyOrder(o *entity.Order) *entity.Order {
	cp := *o
	return &cp
}
