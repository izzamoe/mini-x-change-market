// Package main integration tests start the full in-memory exchange and verify
// the end-to-end flow: REST order creation → matching engine → trade event →
// WebSocket broadcast → REST query.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/izzam/mini-exchange/internal/app/market"
	"github.com/izzam/mini-exchange/internal/app/order"
	"github.com/izzam/mini-exchange/internal/app/trade"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/engine"
	"github.com/izzam/mini-exchange/internal/infra/broker"
	"github.com/izzam/mini-exchange/internal/infra/storage/memory"
	"github.com/izzam/mini-exchange/internal/simulator"
	httptransport "github.com/izzam/mini-exchange/internal/transport/http"
	"github.com/izzam/mini-exchange/internal/transport/ws"
)

// buildTestServer assembles the full in-process server and returns a test HTTP server.
// The caller must call srv.Close() when done.
func buildTestServer(t *testing.T, ctx context.Context) *httptest.Server {
	t.Helper()

	// Storage
	orderRepo := memory.NewOrderRepo()
	tradeRepo := memory.NewTradeRepo()
	marketRepo := memory.NewMarketRepo()

	// Event bus
	eventBus := broker.NewEventBus()
	eventBus.Start()
	t.Cleanup(eventBus.Stop)

	// Stocks
	stocks := entity.DefaultStocks()

	// Engine
	eng := engine.NewEngine(stocks, eventBus, orderRepo, tradeRepo, marketRepo)
	eng.Start(ctx)
	t.Cleanup(eng.Stop)

	// Services
	orderSvc := order.NewService(orderRepo, eng, eventBus)
	tradeSvc := trade.NewService(tradeRepo)
	marketSvc := market.NewService(marketRepo, tradeRepo, eng)

	// Simulator — fast tick for tests
	simCfg := simulator.SimulatorConfig{
		IntervalMin: 50 * time.Millisecond,
		IntervalMax: 100 * time.Millisecond,
	}
	sim := simulator.NewSimulator(stocks, eventBus, marketRepo, simCfg)
	go func() { _ = sim.Start(ctx) }()
	t.Cleanup(sim.Stop)

	// WebSocket hub
	hub := ws.NewHub()
	go hub.Run(ctx)

	// Wire events
	wireEvents(ctx, eventBus, hub, nil, nil, nil, nil)

	// Router
	wsHandler := ws.NewHandler(hub)
	router := httptransport.NewRouter(httptransport.RouterDeps{
		OrderSvc:  orderSvc,
		TradeSvc:  tradeSvc,
		MarketSvc: marketSvc,
		WSHandler: wsHandler,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// ── helpers ───────────────────────────────────────────────────────────────────

func postOrder(t *testing.T, srv *httptest.Server, stockCode, side string, price, qty int64) map[string]interface{} {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"stock_code": stockCode,
		"side":       side,
		"price":      price,
		"quantity":   qty,
		"type":       "LIMIT",
	})
	resp, err := http.Post(srv.URL+"/api/v1/orders", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /orders: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /orders: expected 201, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode order response: %v", err)
	}
	return result
}

func wsConnect(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + srv.URL[4:] + "/ws"
	conn, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func wsSubscribe(t *testing.T, conn *websocket.Conn, channel, stock string) {
	t.Helper()
	msg := map[string]string{"action": "subscribe", "channel": channel, "stock": stock}
	if err := wsjson.Write(context.Background(), conn, msg); err != nil {
		t.Fatalf("ws subscribe: %v", err)
	}
}

// readUntil reads WS messages until fn returns true or timeout expires.
func readUntil(t *testing.T, conn *websocket.Conn, timeout time.Duration, fn func(map[string]interface{}) bool) map[string]interface{} {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		var msg map[string]interface{}
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			t.Fatalf("ws read: %v", err)
		}
		if fn(msg) {
			return msg
		}
	}
}

// ── integration tests ─────────────────────────────────────────────────────────

func TestIntegration_HealthCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := buildTestServer(t, ctx)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestIntegration_AllTickersEndpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := buildTestServer(t, ctx)

	// Wait for simulator to populate at least one ticker.
	time.Sleep(200 * time.Millisecond)

	resp, err := http.Get(srv.URL + "/api/v1/market/ticker")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestIntegration_WebSocketTickerUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := buildTestServer(t, ctx)

	conn := wsConnect(t, srv)

	// Consume the first message (might be "connected" acknowledgement or first tick).
	// Subscribe to BBCA ticker.
	wsSubscribe(t, conn, "market.ticker", "BBCA")

	// Wait for a subscribed confirmation then a ticker update.
	_ = readUntil(t, conn, 5*time.Second, func(msg map[string]interface{}) bool {
		return msg["type"] == "subscribed"
	})

	// Now wait for an actual ticker event from the simulator.
	tickerMsg := readUntil(t, conn, 5*time.Second, func(msg map[string]interface{}) bool {
		return msg["type"] == "market.ticker" && msg["stock"] == "BBCA"
	})
	if tickerMsg == nil {
		t.Fatal("expected ticker event, got nil")
	}
	t.Logf("received ticker: %v", tickerMsg)
}

func TestIntegration_CreateOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := buildTestServer(t, ctx)

	result := postOrder(t, srv, "BBCA", "BUY", 9500, 100)
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data field, got: %v", result)
	}
	if data["stock_code"] != "BBCA" {
		t.Errorf("expected BBCA, got %v", data["stock_code"])
	}
	if data["side"] != "BUY" {
		t.Errorf("expected BUY, got %v", data["side"])
	}
}

func TestIntegration_OrderMatchingProducesTradeEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := buildTestServer(t, ctx)

	// Connect WS and subscribe to trades for BBCA.
	conn := wsConnect(t, srv)
	wsSubscribe(t, conn, "market.trade", "BBCA")

	// Wait for subscribed confirmation.
	_ = readUntil(t, conn, 3*time.Second, func(msg map[string]interface{}) bool {
		return msg["type"] == "subscribed"
	})

	// Submit a BUY and a matching SELL.
	postOrder(t, srv, "BBCA", "BUY", 9500, 100)
	postOrder(t, srv, "BBCA", "SELL", 9500, 100)

	// Expect a trade event via WebSocket.
	tradeMsg := readUntil(t, conn, 5*time.Second, func(msg map[string]interface{}) bool {
		return msg["type"] == "market.trade" && msg["stock"] == "BBCA"
	})
	if tradeMsg == nil {
		t.Fatal("expected trade event, got nil")
	}
	t.Logf("received trade event: %v", tradeMsg)
}

func TestIntegration_TradeHistoryViaREST(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := buildTestServer(t, ctx)

	// Create a matching pair.
	postOrder(t, srv, "BBCA", "BUY", 9500, 100)
	postOrder(t, srv, "BBCA", "SELL", 9500, 100)

	// Give the engine time to match.
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(srv.URL + "/api/v1/market/trades/BBCA")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestIntegration_GetOrderByID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := buildTestServer(t, ctx)

	result := postOrder(t, srv, "BBCA", "BUY", 9500, 100)
	data := result["data"].(map[string]interface{})
	id := data["id"].(string)

	resp, err := http.Get(srv.URL + "/api/v1/orders/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestIntegration_OrderBookEndpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := buildTestServer(t, ctx)

	// Place a BUY order so the order book is non-empty.
	postOrder(t, srv, "BBCA", "BUY", 9500, 100)
	time.Sleep(20 * time.Millisecond)

	resp, err := http.Get(srv.URL + "/api/v1/market/orderbook/BBCA")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestIntegration_MarketTickerForStock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := buildTestServer(t, ctx)

	time.Sleep(200 * time.Millisecond) // let simulator tick

	resp, err := http.Get(srv.URL + "/api/v1/market/ticker/BBCA")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestIntegration_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := buildTestServer(t, ctx)

	// Verify the server is up.
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatal("server not healthy before shutdown")
	}
	_ = resp.Body.Close()

	// Cancel ctx — triggers graceful shutdown of all components.
	cancel()

	// The test server itself is closed by t.Cleanup; verify no panic occurs.
	time.Sleep(50 * time.Millisecond)
}

// Ensure fmt is used to suppress unused import in case of future edits.
var _ = fmt.Sprintf
