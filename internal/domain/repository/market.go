package repository

import (
	"context"

	"github.com/izzam/mini-exchange/internal/domain/entity"
)

// MarketRepository manages the live market snapshot data (tickers and order books).
// Implementations are expected to be highly concurrent and low-latency.
type MarketRepository interface {
	// GetTicker returns the current ticker for a stock, or ErrNotFound.
	GetTicker(ctx context.Context, stockCode string) (*entity.Ticker, error)

	// GetAllTickers returns the current ticker for every registered stock.
	GetAllTickers(ctx context.Context) ([]*entity.Ticker, error)

	// UpdateTicker atomically replaces the ticker snapshot for a stock.
	UpdateTicker(ctx context.Context, ticker *entity.Ticker) error

	// UpdateTickerFromTrade performs an atomic read-modify-write on the ticker
	// for a stock, applying the trade price and quantity via Ticker.UpdateFromTrade.
	// This eliminates the TOCTOU race between the matching engine (which reads
	// the ticker, mutates it, then writes it back) and the simulator (which
	// writes a fresh ticker at any time).  Returns the updated ticker on success.
	UpdateTickerFromTrade(ctx context.Context, stockCode string, price, qty int64) (*entity.Ticker, error)

	// GetOrderBook returns the current order book snapshot for a stock.
	GetOrderBook(ctx context.Context, stockCode string) (*entity.OrderBook, error)

	// UpdateOrderBook atomically replaces the order book snapshot for a stock.
	UpdateOrderBook(ctx context.Context, book *entity.OrderBook) error

	// InitStock creates the initial (empty) ticker and order book entries for a stock.
	// Called once at startup for every registered stock.
	InitStock(ctx context.Context, stock entity.Stock) error
}
