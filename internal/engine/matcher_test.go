package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/event"
	"github.com/izzam/mini-exchange/internal/infra/broker"
	"github.com/izzam/mini-exchange/internal/infra/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// matcherHarness wires up a Matcher with real in-memory repos and an event bus.
type matcherHarness struct {
	matcher    *Matcher
	orderRepo  *memory.OrderRepo
	tradeRepo  *memory.TradeRepo
	marketRepo *memory.MarketRepo
	bus        *broker.EventBus
	ctx        context.Context
	cancel     context.CancelFunc
}

func newMatcherHarness(t *testing.T, stockCode string) *matcherHarness {
	t.Helper()

	orderRepo := memory.NewOrderRepo()
	tradeRepo := memory.NewTradeRepo()
	marketRepo := memory.NewMarketRepo()
	bus := broker.NewEventBus()
	bus.Start()

	ctx := context.Background()
	require.NoError(t, marketRepo.InitStock(ctx, entity.Stock{
		Code: stockCode, BasePrice: 9500, TickSize: 25,
	}))

	ctx2, cancel := context.WithCancel(ctx)
	m := newMatcher(stockCode, bus, orderRepo, tradeRepo, marketRepo)

	return &matcherHarness{
		matcher:    m,
		orderRepo:  orderRepo,
		tradeRepo:  tradeRepo,
		marketRepo: marketRepo,
		bus:        bus,
		ctx:        ctx2,
		cancel:     cancel,
	}
}

func (h *matcherHarness) stop() {
	h.cancel()
	h.bus.Stop()
}

// runOrder saves an order and synchronously runs matchOrder (no goroutine).
func (h *matcherHarness) runOrder(t *testing.T, o *entity.Order) {
	t.Helper()
	require.NoError(t, h.orderRepo.Save(h.ctx, o))
	h.matcher.matchOrder(h.ctx, o)
}

func newOrder(userID, stockCode string, side entity.Side, price, qty int64) *entity.Order {
	return &entity.Order{
		ID:        uuid.NewString(),
		UserID:    userID,
		StockCode: stockCode,
		Side:      side,
		Price:     price,
		Quantity:  qty,
		Status:    entity.OrderStatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// ─── Exact match ─────────────────────────────────────────────────────────────

func TestMatcher_ExactMatch(t *testing.T) {
	h := newMatcherHarness(t, "BBCA")
	defer h.stop()

	sell := newOrder("seller", "BBCA", entity.SideSell, 9500, 100)
	h.runOrder(t, sell)

	buy := newOrder("buyer", "BBCA", entity.SideBuy, 9500, 100)
	h.runOrder(t, buy)

	// Both orders should be FILLED.
	gotSell, err := h.orderRepo.FindByID(h.ctx, sell.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusFilled, gotSell.Status)
	assert.Equal(t, int64(100), gotSell.FilledQuantity)

	gotBuy, err := h.orderRepo.FindByID(h.ctx, buy.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusFilled, gotBuy.Status)

	// Exactly one trade.
	trades, err := h.tradeRepo.FindByStockCode(h.ctx, "BBCA", 10)
	require.NoError(t, err)
	assert.Len(t, trades, 1)
	assert.Equal(t, int64(9500), trades[0].Price)
	assert.Equal(t, int64(100), trades[0].Quantity)
}

// ─── Partial fill ─────────────────────────────────────────────────────────────

func TestMatcher_PartialFill_BuySideRemainder(t *testing.T) {
	h := newMatcherHarness(t, "BBCA")
	defer h.stop()

	// Small sell; large buy — buy remains partial in book.
	sell := newOrder("seller", "BBCA", entity.SideSell, 9500, 50)
	h.runOrder(t, sell)

	buy := newOrder("buyer", "BBCA", entity.SideBuy, 9500, 100)
	h.runOrder(t, buy)

	gotBuy, err := h.orderRepo.FindByID(h.ctx, buy.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusPartial, gotBuy.Status)
	assert.Equal(t, int64(50), gotBuy.FilledQuantity)

	// Buy remainder (50) should still be in the book.
	depth := h.matcher.book.getDepth(5)
	assert.Len(t, depth.Bids, 1)
	assert.Equal(t, int64(50), depth.Bids[0].Quantity)
}

func TestMatcher_PartialFill_SellSideRemainder(t *testing.T) {
	h := newMatcherHarness(t, "BBCA")
	defer h.stop()

	// Large sell, small buy — sell remains partial.
	buy := newOrder("buyer", "BBCA", entity.SideBuy, 9500, 50)
	h.runOrder(t, buy)

	sell := newOrder("seller", "BBCA", entity.SideSell, 9500, 100)
	h.runOrder(t, sell)

	gotSell, err := h.orderRepo.FindByID(h.ctx, sell.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusPartial, gotSell.Status)
	assert.Equal(t, int64(50), gotSell.FilledQuantity)

	depth := h.matcher.book.getDepth(5)
	assert.Len(t, depth.Asks, 1)
	assert.Equal(t, int64(50), depth.Asks[0].Quantity)
}

// ─── No match ─────────────────────────────────────────────────────────────────

func TestMatcher_NoMatch_PriceGap(t *testing.T) {
	h := newMatcherHarness(t, "BBCA")
	defer h.stop()

	// Sell is above buy — no overlap.
	buy := newOrder("buyer", "BBCA", entity.SideBuy, 9400, 100)
	h.runOrder(t, buy)

	sell := newOrder("seller", "BBCA", entity.SideSell, 9500, 100)
	h.runOrder(t, sell)

	trades, err := h.tradeRepo.FindByStockCode(h.ctx, "BBCA", 10)
	require.NoError(t, err)
	assert.Empty(t, trades)

	depth := h.matcher.book.getDepth(5)
	assert.Len(t, depth.Bids, 1)
	assert.Len(t, depth.Asks, 1)
}

// ─── Multi-level match ────────────────────────────────────────────────────────

func TestMatcher_MultiLevelMatch(t *testing.T) {
	h := newMatcherHarness(t, "BBCA")
	defer h.stop()

	// Two resting sells at different prices.
	sell1 := newOrder("s1", "BBCA", entity.SideSell, 9500, 50)
	sell2 := newOrder("s2", "BBCA", entity.SideSell, 9600, 50)
	h.runOrder(t, sell1)
	h.runOrder(t, sell2)

	// Buy at 9600 — should sweep both sell levels.
	buy := newOrder("buyer", "BBCA", entity.SideBuy, 9600, 100)
	h.runOrder(t, buy)

	gotBuy, err := h.orderRepo.FindByID(h.ctx, buy.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusFilled, gotBuy.Status)

	trades, err := h.tradeRepo.FindByStockCode(h.ctx, "BBCA", 10)
	require.NoError(t, err)
	assert.Len(t, trades, 2, "should generate 2 trades for 2 price levels")

	// Order book should be empty after full sweep.
	depth := h.matcher.book.getDepth(5)
	assert.Empty(t, depth.Asks)
}

// ─── FIFO priority ────────────────────────────────────────────────────────────

func TestMatcher_FIFO_SamePrice(t *testing.T) {
	h := newMatcherHarness(t, "BBCA")
	defer h.stop()

	// Two sells at same price; first inserted should match first.
	sell1 := newOrder("s1", "BBCA", entity.SideSell, 9500, 100)
	sell2 := newOrder("s2", "BBCA", entity.SideSell, 9500, 100)
	h.runOrder(t, sell1)
	h.runOrder(t, sell2)

	// Buy only enough to fill the first sell.
	buy := newOrder("buyer", "BBCA", entity.SideBuy, 9500, 100)
	h.runOrder(t, buy)

	gotSell1, err := h.orderRepo.FindByID(h.ctx, sell1.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusFilled, gotSell1.Status, "first SELL must be filled (FIFO)")

	gotSell2, err := h.orderRepo.FindByID(h.ctx, sell2.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusOpen, gotSell2.Status, "second SELL must remain open")
}

// ─── Price improvement ────────────────────────────────────────────────────────

func TestMatcher_PriceImprovement(t *testing.T) {
	h := newMatcherHarness(t, "BBCA")
	defer h.stop()

	// Resting sell at 9500; aggressive buy at 9600 → trade executes at 9500 (passive price).
	sell := newOrder("seller", "BBCA", entity.SideSell, 9500, 100)
	h.runOrder(t, sell)

	buy := newOrder("buyer", "BBCA", entity.SideBuy, 9600, 100)
	h.runOrder(t, buy)

	trades, err := h.tradeRepo.FindByStockCode(h.ctx, "BBCA", 10)
	require.NoError(t, err)
	require.Len(t, trades, 1)
	assert.Equal(t, int64(9500), trades[0].Price, "trade must execute at passive (sell) price")
}

// ─── Events emitted ──────────────────────────────────────────────────────────

func TestMatcher_EventsEmitted(t *testing.T) {
	h := newMatcherHarness(t, "BBCA")
	defer h.stop()

	var mu sync.Mutex
	counts := map[event.Type]int{}
	for _, et := range []event.Type{event.TradeExecuted, event.OrderUpdated, event.TickerUpdated, event.OrderBookUpdated} {
		et := et
		h.bus.Subscribe(et, func(e event.Event) {
			mu.Lock()
			counts[et]++
			mu.Unlock()
		})
	}

	sell := newOrder("seller", "BBCA", entity.SideSell, 9500, 100)
	h.runOrder(t, sell)

	buy := newOrder("buyer", "BBCA", entity.SideBuy, 9500, 100)
	h.runOrder(t, buy)

	// Give the event bus goroutine a moment to deliver.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, counts[event.TradeExecuted], 1, "at least 1 TradeExecuted")
	assert.GreaterOrEqual(t, counts[event.OrderUpdated], 2, "at least 2 OrderUpdated (one per side)")
	assert.GreaterOrEqual(t, counts[event.TickerUpdated], 1, "at least 1 TickerUpdated")
	assert.GreaterOrEqual(t, counts[event.OrderBookUpdated], 1, "at least 1 OrderBookUpdated")
}

// ─── Ticker update ────────────────────────────────────────────────────────────

func TestMatcher_TickerUpdated_AfterTrade(t *testing.T) {
	h := newMatcherHarness(t, "BBCA")
	defer h.stop()

	sell := newOrder("seller", "BBCA", entity.SideSell, 9500, 200)
	h.runOrder(t, sell)

	buy := newOrder("buyer", "BBCA", entity.SideBuy, 9500, 200)
	h.runOrder(t, buy)

	ticker, err := h.marketRepo.GetTicker(h.ctx, "BBCA")
	require.NoError(t, err)
	assert.Equal(t, int64(9500), ticker.LastPrice)
	assert.Equal(t, int64(200), ticker.Volume)
	assert.Equal(t, int64(9500), ticker.High)
	assert.Equal(t, int64(9500), ticker.Low)
}
