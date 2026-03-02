// Package repository defines the persistence interfaces for the domain layer.
// Concrete implementations (in-memory, PostgreSQL, Redis) live in internal/infra/storage.
package repository

import (
	"context"

	"github.com/izzam/mini-exchange/internal/domain/entity"
)

// OrderRepository persists and retrieves Order entities.
type OrderRepository interface {
	// Save inserts a new order. Returns an error if the ID already exists.
	Save(ctx context.Context, order *entity.Order) error

	// FindByID returns the order with the given ID, or ErrNotFound.
	FindByID(ctx context.Context, id string) (*entity.Order, error)

	// FindAll returns orders matching the filter with pagination.
	// The second return value is the total count (before pagination).
	FindAll(ctx context.Context, filter entity.OrderFilter) ([]*entity.Order, int64, error)

	// Update persists changes to an existing order (status, filled_quantity, updated_at).
	Update(ctx context.Context, order *entity.Order) error
}
