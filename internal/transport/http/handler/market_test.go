package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	appmarket "github.com/izzam/mini-exchange/internal/app/market"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/engine"
	"github.com/izzam/mini-exchange/internal/infra/broker"
	"github.com/izzam/mini-exchange/internal/infra/storage/memory"
	"github.com/izzam/mini-exchange/internal/transport/http/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var marketStocks = []entity.Stock{
	{Code: "BBCA", Name: "Bank BCA", BasePrice: 9500, TickSize: 25},
	{Code: "BBRI", Name: "Bank BRI", BasePrice: 5000, TickSize: 25},
}

func newMarketHarness(t *testing.T) (*handler.MarketHandler, *memory.MarketRepo, *memory.TradeRepo, func()) {
	t.Helper()

	orderRepo := memory.NewOrderRepo()
	tradeRepo := memory.NewTradeRepo()
	marketRepo := memory.NewMarketRepo()
	bus := broker.NewEventBus()
	bus.Start()

	ctx := context.Background()
	for _, s := range marketStocks {
		require.NoError(t, marketRepo.InitStock(ctx, s))
	}

	eng := engine.NewEngine(marketStocks, bus, orderRepo, tradeRepo, marketRepo)
	eng.Start(ctx)

	svc := appmarket.NewService(marketRepo, tradeRepo, eng)
	h := handler.NewMarketHandler(svc)

	cleanup := func() {
		eng.Stop()
		bus.Stop()
	}
	return h, marketRepo, tradeRepo, cleanup
}

func TestMarketHandler_AllTickers(t *testing.T) {
	h, _, _, cleanup := newMarketHarness(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/market/ticker", nil)
	w := httptest.NewRecorder()
	h.AllTickers(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var env apiEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.True(t, env.Success)
}

func TestMarketHandler_Ticker_Found(t *testing.T) {
	h, marketRepo, _, cleanup := newMarketHarness(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, marketRepo.UpdateTicker(ctx, &entity.Ticker{
		StockCode: "BBCA", LastPrice: 9600,
	}))

	router := chi.NewRouter()
	router.Get("/market/ticker/{stock}", h.Ticker)

	req := httptest.NewRequest(http.MethodGet, "/market/ticker/BBCA", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMarketHandler_Ticker_NotFound(t *testing.T) {
	h, _, _, cleanup := newMarketHarness(t)
	defer cleanup()

	router := chi.NewRouter()
	router.Get("/market/ticker/{stock}", h.Ticker)

	req := httptest.NewRequest(http.MethodGet, "/market/ticker/UNKNOWN", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMarketHandler_OrderBook(t *testing.T) {
	h, _, _, cleanup := newMarketHarness(t)
	defer cleanup()

	router := chi.NewRouter()
	router.Get("/market/orderbook/{stock}", h.OrderBook)

	req := httptest.NewRequest(http.MethodGet, "/market/orderbook/BBCA", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMarketHandler_OrderBook_NotFound(t *testing.T) {
	h, _, _, cleanup := newMarketHarness(t)
	defer cleanup()

	router := chi.NewRouter()
	router.Get("/market/orderbook/{stock}", h.OrderBook)

	req := httptest.NewRequest(http.MethodGet, "/market/orderbook/UNKNOWN", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMarketHandler_RecentTrades(t *testing.T) {
	h, _, tradeRepo, cleanup := newMarketHarness(t)
	defer cleanup()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		require.NoError(t, tradeRepo.Save(ctx, &entity.Trade{
			ID:        fmt.Sprintf("t%d", i),
			StockCode: "BBCA",
			Price:     9500,
			Quantity:  100,
		}))
	}

	router := chi.NewRouter()
	router.Get("/market/trades/{stock}", h.RecentTrades)

	req := httptest.NewRequest(http.MethodGet, "/market/trades/BBCA?limit=3", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var env apiEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.True(t, env.Success)

	var trades []json.RawMessage
	require.NoError(t, json.Unmarshal(env.Data, &trades))
	assert.Len(t, trades, 3)
}
