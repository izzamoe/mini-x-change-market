package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/event"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

const (
	// matcherChanCap is the depth of each per-stock order channel.
	// If this fills up, SubmitOrder returns a backpressure error.
	matcherChanCap = 1000

	// depthLevels is the number of price levels included in order book snapshots.
	depthLevels = 10
)

// Matcher is the per-stock matching engine.
//
// It runs as a single goroutine and is the only accessor of its orderBook,
// so no mutexes are needed inside the hot path.
type Matcher struct {
	stockCode  string
	book       *orderBook
	ch         chan *entity.Order
	bus        event.Bus
	orderRepo  repository.OrderRepository
	tradeRepo  repository.TradeRepository
	marketRepo repository.MarketRepository
}

// newMatcher creates a Matcher for the given stock.
func newMatcher(
	stockCode string,
	bus event.Bus,
	orderRepo repository.OrderRepository,
	tradeRepo repository.TradeRepository,
	marketRepo repository.MarketRepository,
) *Matcher {
	return &Matcher{
		stockCode:  stockCode,
		book:       newOrderBook(stockCode),
		ch:         make(chan *entity.Order, matcherChanCap),
		bus:        bus,
		orderRepo:  orderRepo,
		tradeRepo:  tradeRepo,
		marketRepo: marketRepo,
	}
}

// submit enqueues an order for matching. It is non-blocking.
// Returns false if the channel is full (backpressure signal).
func (m *Matcher) submit(o *entity.Order) bool {
	select {
	case m.ch <- o:
		return true
	default:
		return false
	}
}

// run is the goroutine body. It exits when ch is closed.
// Context is threaded through for storage calls but shutdown is
// driven purely by closing m.ch so there is no dual-shutdown race.
// Storage operations use context.Background() so that a cancelled ctx
// (shutdown signal) does not abort in-flight writes.
func (m *Matcher) run(ctx context.Context) {
	_ = ctx // kept for interface compatibility; storage uses background context
	storageCtx := context.Background()
	for o := range m.ch {
		if o == nil {
			continue
		}
		m.matchOrder(storageCtx, o)
	}
}

// matchOrder implements price-time priority FIFO matching.
//
//	BUY  order: match against asks ascending  (lowest ask first)
//	SELL order: match against bids descending (highest bid first)
//
// After exhausting matches, any remaining quantity rests in the book.
// Events emitted: TradeExecuted, OrderUpdated (both sides), OrderBookUpdated.
func (m *Matcher) matchOrder(ctx context.Context, incoming *entity.Order) {
	switch incoming.Side {
	case entity.SideBuy:
		m.matchBuy(ctx, incoming)
	case entity.SideSell:
		m.matchSell(ctx, incoming)
	}

	// If there is remaining quantity, the order rests in the book.
	if incoming.IsActive() && incoming.RemainingQuantity() > 0 {
		m.book.addOrder(incoming)
	}

	// Publish updated order book snapshot.
	m.publishOrderBook(ctx)
}

// matchBuy matches a buy order against resting asks.
func (m *Matcher) matchBuy(ctx context.Context, buy *entity.Order) {
	for {
		best := m.book.bestAsk()
		if best == nil {
			break
		}
		if best.order.Price > buy.Price {
			break // No price overlap.
		}
		if buy.RemainingQuantity() <= 0 {
			break
		}

		qty := min64(buy.RemainingQuantity(), best.remaining)
		tradePrice := best.order.Price // passive order sets the price

		m.executeTrade(ctx, buy, best.order, qty, tradePrice)

		// Update resting ask.
		best.remaining -= qty
		if best.remaining <= 0 {
			m.book.removeOrder(best.order.ID)
		}
	}
}

// matchSell matches a sell order against resting bids.
func (m *Matcher) matchSell(ctx context.Context, sell *entity.Order) {
	for {
		best := m.book.bestBid()
		if best == nil {
			break
		}
		if best.order.Price < sell.Price {
			break // No price overlap.
		}
		if sell.RemainingQuantity() <= 0 {
			break
		}

		qty := min64(sell.RemainingQuantity(), best.remaining)
		tradePrice := best.order.Price // passive order sets the price

		m.executeTrade(ctx, best.order, sell, qty, tradePrice)

		// Update resting bid.
		best.remaining -= qty
		if best.remaining <= 0 {
			m.book.removeOrder(best.order.ID)
		}
	}
}

// executeTrade records a matched trade, updates both orders, and publishes events.
func (m *Matcher) executeTrade(ctx context.Context, buy, sell *entity.Order, qty, price int64) {
	// Persist trade.
	trade := &entity.Trade{
		ID:           uuid.NewString(),
		StockCode:    m.stockCode,
		BuyOrderID:   buy.ID,
		SellOrderID:  sell.ID,
		BuyerUserID:  buy.UserID,
		SellerUserID: sell.UserID,
		Price:        price,
		Quantity:     qty,
		ExecutedAt:   time.Now(),
	}
	if err := m.tradeRepo.Save(ctx, trade); err != nil {
		slog.Error("matcher: failed to save trade", "err", err, "stock", m.stockCode)
		// Do not fill orders when trade persistence failed — the trade would
		// be lost but the orders' filled quantities would be inconsistent.
		return
	}

	// Update both orders.
	buy.Fill(qty)
	sell.Fill(qty)

	if err := m.orderRepo.Update(ctx, buy); err != nil {
		slog.Error("matcher: failed to update buy order", "err", err, "id", buy.ID)
	}
	if err := m.orderRepo.Update(ctx, sell); err != nil {
		slog.Error("matcher: failed to update sell order", "err", err, "id", sell.ID)
	}

	// Update ticker.
	m.updateTicker(ctx, price, qty)

	// Publish domain events.
	m.bus.Publish(event.Event{
		Type:      event.TradeExecuted,
		StockCode: m.stockCode,
		Payload:   trade,
		Timestamp: trade.ExecutedAt,
	})
	m.bus.Publish(event.Event{
		Type:      event.OrderUpdated,
		StockCode: m.stockCode,
		UserID:    buy.UserID,
		Payload:   buy,
		Timestamp: trade.ExecutedAt,
	})
	m.bus.Publish(event.Event{
		Type:      event.OrderUpdated,
		StockCode: m.stockCode,
		UserID:    sell.UserID,
		Payload:   sell,
		Timestamp: trade.ExecutedAt,
	})
}

// updateTicker refreshes the market ticker snapshot after a trade.
// It uses UpdateTickerFromTrade to perform an atomic read-modify-write,
// eliminating the TOCTOU race with the simulator goroutine.
func (m *Matcher) updateTicker(ctx context.Context, price, qty int64) {
	ticker, err := m.marketRepo.UpdateTickerFromTrade(ctx, m.stockCode, price, qty)
	if err != nil {
		slog.Error("matcher: failed to update ticker from trade", "err", err, "stock", m.stockCode)
		return
	}
	m.bus.Publish(event.Event{
		Type:      event.TickerUpdated,
		StockCode: m.stockCode,
		Payload:   ticker,
		Timestamp: ticker.UpdatedAt,
	})
}

// publishOrderBook builds a depth snapshot and publishes it.
func (m *Matcher) publishOrderBook(ctx context.Context) {
	snapshot := m.book.getDepth(depthLevels)
	snapshot.UpdatedAt = time.Now()

	if err := m.marketRepo.UpdateOrderBook(ctx, snapshot); err != nil {
		slog.Error("matcher: failed to update order book", "err", err, "stock", m.stockCode)
	}

	m.bus.Publish(event.Event{
		Type:      event.OrderBookUpdated,
		StockCode: m.stockCode,
		Payload:   snapshot,
		Timestamp: snapshot.UpdatedAt,
	})
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
