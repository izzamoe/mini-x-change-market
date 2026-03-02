package engine

import (
	"context"
	"testing"
	"time"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/infra/broker"
	"github.com/izzam/mini-exchange/internal/infra/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testStocks = []entity.Stock{
	{Code: "BBCA", Name: "Bank BCA", BasePrice: 9500, TickSize: 25},
	{Code: "BBRI", Name: "Bank BRI", BasePrice: 5000, TickSize: 25},
}

func newEngineHarness(t *testing.T) (*Engine, *memory.OrderRepo, *memory.TradeRepo, *memory.MarketRepo, *broker.EventBus) {
	t.Helper()

	orderRepo := memory.NewOrderRepo()
	tradeRepo := memory.NewTradeRepo()
	marketRepo := memory.NewMarketRepo()
	bus := broker.NewEventBus()
	bus.Start()

	ctx := context.Background()
	for _, s := range testStocks {
		require.NoError(t, marketRepo.InitStock(ctx, s))
	}

	eng := NewEngine(testStocks, bus, orderRepo, tradeRepo, marketRepo)
	return eng, orderRepo, tradeRepo, marketRepo, bus
}

func TestEngine_SubmitOrder_RoutesByStock(t *testing.T) {
	eng, orderRepo, tradeRepo, _, bus := newEngineHarness(t)
	ctx := context.Background()
	eng.Start(ctx)
	defer func() {
		eng.Stop()
		bus.Stop()
	}()

	sell := newOrder("s1", "BBCA", entity.SideSell, 9500, 100)
	require.NoError(t, orderRepo.Save(ctx, sell))
	require.NoError(t, eng.SubmitOrder(sell))

	buy := newOrder("b1", "BBCA", entity.SideBuy, 9500, 100)
	require.NoError(t, orderRepo.Save(ctx, buy))
	require.NoError(t, eng.SubmitOrder(buy))

	// Give matcher goroutine time to process.
	time.Sleep(30 * time.Millisecond)

	trades, err := tradeRepo.FindByStockCode(ctx, "BBCA", 10)
	require.NoError(t, err)
	assert.Len(t, trades, 1, "orders should have matched")
}

func TestEngine_SubmitOrder_UnknownStock(t *testing.T) {
	eng, _, _, _, bus := newEngineHarness(t)
	ctx := context.Background()
	eng.Start(ctx)
	defer func() {
		eng.Stop()
		bus.Stop()
	}()

	unknown := newOrder("u1", "XXXX", entity.SideBuy, 1000, 10)
	err := eng.SubmitOrder(unknown)
	assert.Error(t, err, "unknown stock should return error")
}

func TestEngine_SubmitOrder_Backpressure(t *testing.T) {
	eng, _, _, _, bus := newEngineHarness(t)
	// Do NOT start the engine — channel drains won't happen.
	defer bus.Stop()

	// Fill the BBCA matcher channel completely.
	for i := 0; i < matcherChanCap; i++ {
		o := newOrder("u1", "BBCA", entity.SideBuy, 9500, 1)
		_ = eng.SubmitOrder(o)
	}

	// Next submit should fail with backpressure.
	o := newOrder("u1", "BBCA", entity.SideBuy, 9500, 1)
	err := eng.SubmitOrder(o)
	assert.Error(t, err, "should return backpressure error when channel is full")
}

func TestEngine_GracefulShutdown(t *testing.T) {
	eng, orderRepo, _, _, bus := newEngineHarness(t)
	ctx := context.Background()
	eng.Start(ctx)

	// Submit a few orders.
	for i := 0; i < 5; i++ {
		o := newOrder("u1", "BBCA", entity.SideBuy, 9500, 10)
		require.NoError(t, orderRepo.Save(ctx, o))
		_ = eng.SubmitOrder(o)
	}

	// Stop should complete without hanging.
	done := make(chan struct{})
	go func() {
		eng.Stop()
		bus.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Engine.Stop() did not complete in time")
	}
}

func TestEngine_GetOrderBook(t *testing.T) {
	eng, orderRepo, _, _, bus := newEngineHarness(t)
	ctx := context.Background()
	eng.Start(ctx)
	defer func() {
		eng.Stop()
		bus.Stop()
	}()

	// Submit a resting order so the book is non-empty.
	o := newOrder("u1", "BBCA", entity.SideBuy, 9400, 100)
	require.NoError(t, orderRepo.Save(ctx, o))
	require.NoError(t, eng.SubmitOrder(o))

	time.Sleep(30 * time.Millisecond)

	book, err := eng.GetOrderBook(ctx, "BBCA")
	require.NoError(t, err)
	assert.Equal(t, "BBCA", book.StockCode)
}

func TestEngine_MultiStock_Isolation(t *testing.T) {
	eng, orderRepo, tradeRepo, _, bus := newEngineHarness(t)
	ctx := context.Background()
	eng.Start(ctx)
	defer func() {
		eng.Stop()
		bus.Stop()
	}()

	// Match on BBCA.
	bbcaSell := newOrder("s1", "BBCA", entity.SideSell, 9500, 100)
	bbcaBuy := newOrder("b1", "BBCA", entity.SideBuy, 9500, 100)
	require.NoError(t, orderRepo.Save(ctx, bbcaSell))
	require.NoError(t, orderRepo.Save(ctx, bbcaBuy))
	require.NoError(t, eng.SubmitOrder(bbcaSell))
	require.NoError(t, eng.SubmitOrder(bbcaBuy))

	// Match on BBRI.
	bbriSell := newOrder("s2", "BBRI", entity.SideSell, 5000, 50)
	bbriBuy := newOrder("b2", "BBRI", entity.SideBuy, 5000, 50)
	require.NoError(t, orderRepo.Save(ctx, bbriSell))
	require.NoError(t, orderRepo.Save(ctx, bbriBuy))
	require.NoError(t, eng.SubmitOrder(bbriSell))
	require.NoError(t, eng.SubmitOrder(bbriBuy))

	time.Sleep(50 * time.Millisecond)

	bbcaTrades, err := tradeRepo.FindByStockCode(ctx, "BBCA", 10)
	require.NoError(t, err)
	assert.Len(t, bbcaTrades, 1)

	bbriTrades, err := tradeRepo.FindByStockCode(ctx, "BBRI", 10)
	require.NoError(t, err)
	assert.Len(t, bbriTrades, 1)
}
