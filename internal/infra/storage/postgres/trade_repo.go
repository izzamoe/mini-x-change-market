package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

// TradeRepo is a PostgreSQL implementation of repository.TradeRepository.
type TradeRepo struct {
	pool *pgxpool.Pool
}

// NewTradeRepo constructs a TradeRepo backed by pool.
func NewTradeRepo(pool *pgxpool.Pool) *TradeRepo {
	return &TradeRepo{pool: pool}
}

// Save inserts a newly executed trade.
// Returns repository.ErrDuplicate if the ID already exists.
func (r *TradeRepo) Save(ctx context.Context, t *entity.Trade) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO trades
			(id, stock_code, buy_order_id, sell_order_id, price,
			 quantity, buyer_user_id, seller_user_id, executed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		t.ID, t.StockCode, t.BuyOrderID, t.SellOrderID,
		t.Price, t.Quantity, t.BuyerUserID, t.SellerUserID, t.ExecutedAt,
	)
	if err != nil {
		if isDuplicateKey(err) {
			return repository.ErrDuplicate
		}
		return fmt.Errorf("trade save: %w", err)
	}
	return nil
}

// FindByStockCode returns up to limit of the most recent trades for a stock,
// ordered newest-first.
func (r *TradeRepo) FindByStockCode(ctx context.Context, stockCode string, limit int) ([]*entity.Trade, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, stock_code, buy_order_id, sell_order_id, price,
		       quantity, buyer_user_id, seller_user_id, executed_at
		FROM trades
		WHERE stock_code = $1
		ORDER BY executed_at DESC
		LIMIT $2`,
		stockCode, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("trade find by stock: %w", err)
	}
	defer rows.Close()

	return scanTrades(rows)
}

// FindAll returns trades matching filter with pagination.
// The second return value is the total matching count before pagination.
func (r *TradeRepo) FindAll(ctx context.Context, filter entity.TradeFilter) ([]*entity.Trade, int64, error) {
	args := make([]interface{}, 0, 4)
	where := "WHERE 1=1"
	n := 1
	if filter.StockCode != "" {
		where += fmt.Sprintf(" AND stock_code = $%d", n)
		args = append(args, filter.StockCode)
		n++
	}
	if filter.UserID != "" {
		where += fmt.Sprintf(" AND (buyer_user_id = $%d OR seller_user_id = $%d)", n, n)
		args = append(args, filter.UserID)
		n++
	}

	var total int64
	if err := r.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM trades "+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("trade count: %w", err)
	}

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
		SELECT id, stock_code, buy_order_id, sell_order_id, price,
		       quantity, buyer_user_id, seller_user_id, executed_at
		FROM trades
		`+where+`
		ORDER BY executed_at DESC
		LIMIT $`+fmt.Sprint(n)+` OFFSET $`+fmt.Sprint(n+1),
		args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("trade find all: %w", err)
	}
	defer rows.Close()

	result, err := scanTrades(rows)
	if err != nil {
		return nil, 0, err
	}
	return result, total, nil
}

// ---  helpers  ---

func scanTrades(rows pgx.Rows) ([]*entity.Trade, error) {
	var result []*entity.Trade
	for rows.Next() {
		var t entity.Trade
		if err := rows.Scan(
			&t.ID, &t.StockCode, &t.BuyOrderID, &t.SellOrderID,
			&t.Price, &t.Quantity, &t.BuyerUserID, &t.SellerUserID, &t.ExecutedAt,
		); err != nil {
			return nil, fmt.Errorf("trade scan: %w", err)
		}
		result = append(result, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("trade rows: %w", err)
	}
	if result == nil {
		result = []*entity.Trade{}
	}
	return result, nil
}
