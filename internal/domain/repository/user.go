package repository

import (
	"context"

	"github.com/izzam/mini-exchange/internal/domain/entity"
)

// UserRepository defines persistence operations for users.
type UserRepository interface {
	// Save inserts a new user. Returns ErrDuplicate if the username already exists.
	Save(ctx context.Context, u *entity.User) error

	// FindByUsername returns the user with the given username, or ErrNotFound.
	FindByUsername(ctx context.Context, username string) (*entity.User, error)

	// FindAll returns every user in the store (used to warm the in-memory cache).
	FindAll(ctx context.Context) ([]*entity.User, error)
}
