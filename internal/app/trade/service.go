// Package trade contains the application service for trade history.
package trade

import (
	"context"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

// Service orchestrates trade history queries.
type Service struct {
	tradeRepo repository.TradeRepository
}

// NewService constructs a trade Service.
func NewService(tradeRepo repository.TradeRepository) *Service {
	return &Service{tradeRepo: tradeRepo}
}

// GetTradeHistory returns a filtered, paginated list of trades.
func (s *Service) GetTradeHistory(ctx context.Context, filter entity.TradeFilter) ([]*entity.Trade, int64, error) {
	return s.tradeRepo.FindAll(ctx, filter)
}

// GetTradesByStock returns the most recent trades for a stock, up to limit.
func (s *Service) GetTradesByStock(ctx context.Context, stockCode string, limit int) ([]*entity.Trade, error) {
	return s.tradeRepo.FindByStockCode(ctx, stockCode, limit)
}
