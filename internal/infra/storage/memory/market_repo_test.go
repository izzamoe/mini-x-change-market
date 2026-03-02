package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testStock = entity.Stock{Code: "BBCA", Name: "Bank BCA", BasePrice: 9500, TickSize: 25}

func TestMarketRepo_InitStock(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()

	require.NoError(t, r.InitStock(ctx, testStock))

	ticker, err := r.GetTicker(ctx, "BBCA")
	require.NoError(t, err)
	assert.Equal(t, "BBCA", ticker.StockCode)

	book, err := r.GetOrderBook(ctx, "BBCA")
	require.NoError(t, err)
	assert.Equal(t, "BBCA", book.StockCode)
	assert.Empty(t, book.Bids)
	assert.Empty(t, book.Asks)
}

func TestMarketRepo_InitStock_Idempotent(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()

	require.NoError(t, r.InitStock(ctx, testStock))

	// Set a ticker value.
	ticker := &entity.Ticker{StockCode: "BBCA", LastPrice: 9600, UpdatedAt: time.Now()}
	require.NoError(t, r.UpdateTicker(ctx, ticker))

	// Second InitStock should not overwrite the existing ticker.
	require.NoError(t, r.InitStock(ctx, testStock))

	got, err := r.GetTicker(ctx, "BBCA")
	require.NoError(t, err)
	assert.Equal(t, int64(9600), got.LastPrice, "InitStock should not overwrite existing ticker")
}

func TestMarketRepo_GetTicker_NotFound(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()

	_, err := r.GetTicker(ctx, "NONEXISTENT")
	assert.ErrorIs(t, err, repository.ErrNotFound)
}

func TestMarketRepo_UpdateTicker(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()

	require.NoError(t, r.InitStock(ctx, testStock))

	ticker := &entity.Ticker{
		StockCode: "BBCA",
		LastPrice: 9600,
		High:      9700,
		Low:       9400,
		Volume:    50000,
		UpdatedAt: time.Now(),
	}
	require.NoError(t, r.UpdateTicker(ctx, ticker))

	got, err := r.GetTicker(ctx, "BBCA")
	require.NoError(t, err)
	assert.Equal(t, int64(9600), got.LastPrice)
	assert.Equal(t, int64(9700), got.High)
	assert.Equal(t, int64(50000), got.Volume)
}

func TestMarketRepo_UpdateTicker_NotFound(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()

	err := r.UpdateTicker(ctx, &entity.Ticker{StockCode: "NONEXISTENT"})
	assert.ErrorIs(t, err, repository.ErrNotFound)
}

func TestMarketRepo_GetAllTickers(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()

	stocks := []entity.Stock{
		{Code: "BBCA", Name: "Bank BCA", BasePrice: 9500, TickSize: 25},
		{Code: "BBRI", Name: "Bank BRI", BasePrice: 5000, TickSize: 25},
		{Code: "TLKM", Name: "Telkom", BasePrice: 3500, TickSize: 25},
	}
	for _, s := range stocks {
		require.NoError(t, r.InitStock(ctx, s))
	}

	tickers, err := r.GetAllTickers(ctx)
	require.NoError(t, err)
	assert.Len(t, tickers, 3)
}

func TestMarketRepo_UpdateOrderBook(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()

	require.NoError(t, r.InitStock(ctx, testStock))

	book := &entity.OrderBook{
		StockCode: "BBCA",
		Bids: []entity.PriceLevel{
			{Price: 9500, Quantity: 100, Count: 2},
			{Price: 9475, Quantity: 200, Count: 3},
		},
		Asks: []entity.PriceLevel{
			{Price: 9525, Quantity: 150, Count: 1},
		},
		UpdatedAt: time.Now(),
	}
	require.NoError(t, r.UpdateOrderBook(ctx, book))

	got, err := r.GetOrderBook(ctx, "BBCA")
	require.NoError(t, err)
	assert.Len(t, got.Bids, 2)
	assert.Len(t, got.Asks, 1)
	assert.Equal(t, int64(9500), got.BestBid().Price)
	assert.Equal(t, int64(9525), got.BestAsk().Price)
}

func TestMarketRepo_UpdateOrderBook_NotFound(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()

	err := r.UpdateOrderBook(ctx, &entity.OrderBook{StockCode: "NONEXISTENT"})
	assert.ErrorIs(t, err, repository.ErrNotFound)
}

func TestMarketRepo_OrderBook_IsolatesCallerMutations(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()

	require.NoError(t, r.InitStock(ctx, testStock))

	book := &entity.OrderBook{
		StockCode: "BBCA",
		Bids:      []entity.PriceLevel{{Price: 9500, Quantity: 100}},
		Asks:      []entity.PriceLevel{},
		UpdatedAt: time.Now(),
	}
	require.NoError(t, r.UpdateOrderBook(ctx, book))

	// Mutate the slice we passed in — should NOT affect stored data.
	book.Bids[0].Price = 99999

	got, err := r.GetOrderBook(ctx, "BBCA")
	require.NoError(t, err)
	assert.Equal(t, int64(9500), got.Bids[0].Price, "stored data must not be affected by caller mutation")
}

func TestMarketRepo_ConcurrentTickerUpdates(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()
	require.NoError(t, r.InitStock(ctx, testStock))

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Concurrent writers.
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			_ = r.UpdateTicker(ctx, &entity.Ticker{
				StockCode: "BBCA",
				LastPrice: int64(9000 + i),
				UpdatedAt: time.Now(),
			})
		}(i)
	}

	// Concurrent readers.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = r.GetTicker(ctx, "BBCA")
		}()
	}
	wg.Wait()
}

func TestMarketRepo_ConcurrentOrderBookUpdates(t *testing.T) {
	r := NewMarketRepo()
	ctx := context.Background()
	require.NoError(t, r.InitStock(ctx, testStock))

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			_ = r.UpdateOrderBook(ctx, &entity.OrderBook{
				StockCode: "BBCA",
				Bids:      []entity.PriceLevel{{Price: int64(9000 + i), Quantity: 100}},
				Asks:      []entity.PriceLevel{},
				UpdatedAt: time.Now(),
			})
		}(i)
		go func() {
			defer wg.Done()
			_, _ = r.GetOrderBook(ctx, "BBCA")
		}()
	}
	wg.Wait()
}
