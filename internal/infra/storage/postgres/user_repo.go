package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

// UserRepo is a PostgreSQL implementation of repository.UserRepository.
type UserRepo struct {
	pool *pgxpool.Pool
}

// NewUserRepo constructs a UserRepo backed by pool.
func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

// Save inserts a new user into the users table.
// Returns repository.ErrDuplicate if the username already exists.
func (r *UserRepo) Save(ctx context.Context, u *entity.User) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, username, password_hash, created_at)
		VALUES ($1, $2, $3, $4)`,
		u.ID, u.Username, u.PasswordHash, u.CreatedAt,
	)
	if err != nil {
		if isDuplicateKey(err) {
			return repository.ErrDuplicate
		}
		return fmt.Errorf("user save: %w", err)
	}
	return nil
}

// FindByUsername returns the user with the given username, or repository.ErrNotFound.
func (r *UserRepo) FindByUsername(ctx context.Context, username string) (*entity.User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, created_at
		FROM users
		WHERE username = $1`, username)

	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("user find by username: %w", err)
	}
	return u, nil
}

// FindAll returns all users. Used to warm the in-memory cache on startup.
func (r *UserRepo) FindAll(ctx context.Context) ([]*entity.User, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, username, password_hash, created_at
		FROM users
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("user find all: %w", err)
	}
	defer rows.Close()

	var users []*entity.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("user scan: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// scanUser reads one user row.
func scanUser(row interface {
	Scan(...interface{}) error
}) (*entity.User, error) {
	var u entity.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
