package repository

import (
	"context"

	"github.com/izzam/mini-exchange/internal/domain/entity"
)

// TradeRepository persists and retrieves Trade entities.
type TradeRepository interface {
	// Save inserts a newly executed trade.
	Save(ctx context.Context, trade *entity.Trade) error

	// FindByStockCode returns the most recent trades for a stock, limited to n.
	FindByStockCode(ctx context.Context, stockCode string, limit int) ([]*entity.Trade, error)

	// FindAll returns trades matching the filter with pagination.
	// The second return value is the total count (before pagination).
	FindAll(ctx context.Context, filter entity.TradeFilter) ([]*entity.Trade, int64, error)
}
