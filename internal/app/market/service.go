// Package market contains the application service for market data.
package market

import (
	"context"
	"log/slog"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
	"github.com/izzam/mini-exchange/internal/engine"
)

// MarketCache is an optional read-through cache (e.g. Redis) for hot market
// data. A nil implementation disables caching.  Cache misses (nil, nil)
// fall through to the in-memory repository.
type MarketCache interface {
	GetTicker(ctx context.Context, stock string) (*entity.Ticker, error)
	GetOrderBook(ctx context.Context, stock string) (*entity.OrderBook, error)
}

// Service orchestrates market-data queries (tickers, order books, recent trades).
type Service struct {
	marketRepo repository.MarketRepository
	tradeRepo  repository.TradeRepository
	engine     *engine.Engine
	cache      MarketCache // optional; nil = cache disabled
}

// NewService constructs a market Service.
func NewService(
	marketRepo repository.MarketRepository,
	tradeRepo repository.TradeRepository,
	eng *engine.Engine,
) *Service {
	return &Service{
		marketRepo: marketRepo,
		tradeRepo:  tradeRepo,
		engine:     eng,
	}
}

// SetCache attaches an optional read-through cache. Call this after
// construction when Redis is available.
func (s *Service) SetCache(c MarketCache) {
	s.cache = c
}

// GetTicker returns the current ticker snapshot for a stock.
// Cache-aside: tries the cache first; falls back to the in-memory repo on
// miss or error.
func (s *Service) GetTicker(ctx context.Context, stockCode string) (*entity.Ticker, error) {
	if s.cache != nil {
		t, err := s.cache.GetTicker(ctx, stockCode)
		if err != nil {
			slog.Debug("redis cache GetTicker error, falling back", "stock", stockCode, "error", err)
		} else if t != nil {
			return t, nil // cache hit
		}
	}
	return s.marketRepo.GetTicker(ctx, stockCode)
}

// GetAllTickers returns the current ticker snapshot for every registered stock.
// The full list is always sourced from the in-memory repo (it holds all stocks).
func (s *Service) GetAllTickers(ctx context.Context) ([]*entity.Ticker, error) {
	return s.marketRepo.GetAllTickers(ctx)
}

// GetOrderBook returns the current order book depth snapshot for a stock.
// Cache-aside: tries the cache first; falls back to the live engine view on
// miss or error.
func (s *Service) GetOrderBook(ctx context.Context, stockCode string) (*entity.OrderBook, error) {
	if s.cache != nil {
		b, err := s.cache.GetOrderBook(ctx, stockCode)
		if err != nil {
			slog.Debug("redis cache GetOrderBook error, falling back", "stock", stockCode, "error", err)
		} else if b != nil {
			return b, nil // cache hit
		}
	}
	return s.engine.GetOrderBook(ctx, stockCode)
}

// GetRecentTrades returns the most recent trades for a stock, up to limit.
func (s *Service) GetRecentTrades(ctx context.Context, stockCode string, limit int) ([]*entity.Trade, error) {
	return s.tradeRepo.FindByStockCode(ctx, stockCode, limit)
}
