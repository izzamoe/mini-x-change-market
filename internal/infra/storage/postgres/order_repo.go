// Package postgres provides PostgreSQL-backed implementations of the domain
// repository interfaces. They are used when STORAGE_TYPE=postgres and serve
// as the primary durable store (versus the in-memory store used by default).
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

// OrderRepo is a PostgreSQL implementation of repository.OrderRepository.
type OrderRepo struct {
	pool *pgxpool.Pool
}

// NewOrderRepo constructs an OrderRepo backed by pool.
func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo {
	return &OrderRepo{pool: pool}
}

// Save inserts a new order. Returns repository.ErrDuplicate if the ID already
// exists (PostgreSQL unique-key violation).
func (r *OrderRepo) Save(ctx context.Context, o *entity.Order) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO orders
			(id, user_id, stock_code, side, price, quantity,
			 filled_quantity, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		o.ID, o.UserID, o.StockCode, string(o.Side),
		o.Price, o.Quantity, o.FilledQuantity, string(o.Status),
		o.CreatedAt, o.UpdatedAt,
	)
	if err != nil {
		if isDuplicateKey(err) {
			return repository.ErrDuplicate
		}
		return fmt.Errorf("order save: %w", err)
	}
	return nil
}

// FindByID returns the order with the given UUID string, or repository.ErrNotFound.
func (r *OrderRepo) FindByID(ctx context.Context, id string) (*entity.Order, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, stock_code, side, price, quantity,
		       filled_quantity, status, created_at, updated_at
		FROM orders
		WHERE id = $1`, id)

	o, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("order find by id: %w", err)
	}
	return o, nil
}

// FindAll returns orders matching filter with pagination.
// The second return value is the total matching count before pagination.
func (r *OrderRepo) FindAll(ctx context.Context, filter entity.OrderFilter) ([]*entity.Order, int64, error) {
	// Build dynamic WHERE clause.
	args := make([]interface{}, 0, 4)
	where := "WHERE 1=1"
	n := 1
	if filter.StockCode != "" {
		where += fmt.Sprintf(" AND stock_code = $%d", n)
		args = append(args, filter.StockCode)
		n++
	}
	if filter.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", n)
		args = append(args, string(filter.Status))
		n++
	}
	if filter.UserID != "" {
		where += fmt.Sprintf(" AND user_id = $%d", n)
		args = append(args, filter.UserID)
		n++
	}

	// Count total matching rows.
	var total int64
	if err := r.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM orders "+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("order count: %w", err)
	}

	// Apply pagination defaults.
	page := filter.Page
	perPage := filter.PerPage
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 20
	}
	offset := (page - 1) * perPage

	args = append(args, perPage, offset)
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, stock_code, side, price, quantity,
		       filled_quantity, status, created_at, updated_at
		FROM orders
		`+where+`
		ORDER BY created_at DESC
		LIMIT $`+fmt.Sprint(n)+` OFFSET $`+fmt.Sprint(n+1),
		args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("order find all: %w", err)
	}
	defer rows.Close()

	var result []*entity.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("order scan: %w", err)
		}
		result = append(result, o)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("order rows: %w", err)
	}
	if result == nil {
		result = []*entity.Order{}
	}
	return result, total, nil
}

// Update persists the mutable fields of an existing order.
// Returns repository.ErrNotFound if no order with that ID exists.
func (r *OrderRepo) Update(ctx context.Context, o *entity.Order) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE orders
		SET filled_quantity = $2,
		    status          = $3,
		    updated_at      = $4
		WHERE id = $1`,
		o.ID, o.FilledQuantity, string(o.Status), o.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("order update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

// ---  helpers  ---

// scanOrder reads one order row from any pgx row-like (QueryRow or rows.Next).
func scanOrder(row interface {
	Scan(...interface{}) error
}) (*entity.Order, error) {
	var (
		o    entity.Order
		side string
		st   string
	)
	err := row.Scan(
		&o.ID, &o.UserID, &o.StockCode, &side,
		&o.Price, &o.Quantity, &o.FilledQuantity, &st,
		&o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	o.Side = entity.Side(side)
	o.Status = entity.OrderStatus(st)
	return &o, nil
}

// isDuplicateKey detects a PostgreSQL unique-key violation (SQLSTATE 23505).
func isDuplicateKey(err error) bool {
	// pgx wraps driver errors; check the SQLSTATE code.
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
