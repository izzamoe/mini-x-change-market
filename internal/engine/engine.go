package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/event"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

// Engine manages all per-stock Matchers and provides the public interface
// for the rest of the application to submit orders and query the engine.
type Engine struct {
	matchers   map[string]*Matcher
	bus        event.Bus
	orderRepo  repository.OrderRepository
	tradeRepo  repository.TradeRepository
	marketRepo repository.MarketRepository
	wg         sync.WaitGroup
	stopOnce   sync.Once
}

// NewEngine creates an Engine and initialises a Matcher for every stock.
func NewEngine(
	stocks []entity.Stock,
	bus event.Bus,
	orderRepo repository.OrderRepository,
	tradeRepo repository.TradeRepository,
	marketRepo repository.MarketRepository,
) *Engine {
	e := &Engine{
		matchers:   make(map[string]*Matcher, len(stocks)),
		bus:        bus,
		orderRepo:  orderRepo,
		tradeRepo:  tradeRepo,
		marketRepo: marketRepo,
	}
	for _, s := range stocks {
		e.matchers[s.Code] = newMatcher(s.Code, bus, orderRepo, tradeRepo, marketRepo)
	}
	return e
}

// Start launches a goroutine for each Matcher. It is non-blocking.
// ctx is passed through to storage calls; shutdown is via Stop().
func (e *Engine) Start(ctx context.Context) {
	for _, m := range e.matchers {
		e.wg.Add(1)
		m := m // capture
		go func() {
			defer e.wg.Done()
			m.run(ctx)
		}()
	}
}

// Stop closes each Matcher's channel (draining any queued orders) and
// waits for all goroutines to exit. Idempotent.
func (e *Engine) Stop() {
	e.stopOnce.Do(func() {
		for _, m := range e.matchers {
			close(m.ch)
		}
		e.wg.Wait()
	})
}

// SubmitOrder routes an order to the correct stock's Matcher channel.
// Returns an error if the stock is unknown or if the channel is full (backpressure).
func (e *Engine) SubmitOrder(order *entity.Order) error {
	m, ok := e.matchers[order.StockCode]
	if !ok {
		return fmt.Errorf("unknown stock: %s", order.StockCode)
	}
	if !m.submit(order) {
		return fmt.Errorf("order queue full for %s: backpressure", order.StockCode)
	}
	return nil
}

// GetOrderBook returns the latest order book snapshot for a stock.
func (e *Engine) GetOrderBook(ctx context.Context, stockCode string) (*entity.OrderBook, error) {
	return e.marketRepo.GetOrderBook(ctx, stockCode)
}
