package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/izzam/mini-exchange/internal/domain/event"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// miniTickerMsg builds a Binance combined-stream message for testing.
func miniTickerMsg(symbol, close, high, low, vol string) binanceStreamMsg {
	return binanceStreamMsg{
		Stream: strings.ToLower(symbol) + "@miniTicker",
		Data: binanceMiniTicker{
			EventType: "24hrMiniTicker",
			Symbol:    strings.ToUpper(symbol),
			Close:     close,
			High:      high,
			Low:       low,
			Volume:    vol,
		},
	}
}

// ── parsePrice / parseVolume unit tests ──────────────────────────────────────

func TestParsePrice_Typical(t *testing.T) {
	t.Parallel()
	got, err := parsePrice("60123.45")
	if err != nil {
		t.Fatal(err)
	}
	// 60123.45 * 100 = 6012345
	if got != 6012345 {
		t.Errorf("expected 6012345, got %d", got)
	}
}

func TestParsePrice_Integer(t *testing.T) {
	t.Parallel()
	got, err := parsePrice("100")
	if err != nil {
		t.Fatal(err)
	}
	if got != 10000 {
		t.Errorf("expected 10000, got %d", got)
	}
}

func TestParsePrice_Invalid(t *testing.T) {
	t.Parallel()
	_, err := parsePrice("not-a-number")
	if err == nil {
		t.Fatal("expected error for invalid price string")
	}
}

func TestParseVolume_Typical(t *testing.T) {
	t.Parallel()
	got, err := parseVolume("12345.67")
	if err != nil {
		t.Fatal(err)
	}
	if got != 12345 {
		t.Errorf("expected 12345, got %d", got)
	}
}

func TestParseVolume_Invalid(t *testing.T) {
	t.Parallel()
	_, err := parseVolume("bad")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── calcBackoff unit tests ────────────────────────────────────────────────────

func TestCalcBackoff_Progression(t *testing.T) {
	t.Parallel()
	max := 30 * time.Second
	expected := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second, // capped
		30 * time.Second,
	}
	for i, want := range expected {
		got := calcBackoff(i, max)
		if got != want {
			t.Errorf("attempt %d: expected %v, got %v", i, want, got)
		}
	}
}

func TestCalcBackoff_NeverExceedsMax(t *testing.T) {
	t.Parallel()
	max := 5 * time.Second
	for attempt := 0; attempt < 20; attempt++ {
		got := calcBackoff(attempt, max)
		if got > max {
			t.Errorf("attempt %d: backoff %v exceeds max %v", attempt, got, max)
		}
	}
}

// ── handleMessage unit tests ──────────────────────────────────────────────────

func TestHandleMessage_KnownSymbol(t *testing.T) {
	t.Parallel()
	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := BinanceConfig{
		WSURL: "",
		Symbols: map[string]string{
			"btcusdt": "BTCUSDT",
		},
		MaxBackoff: 30 * time.Second,
	}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	msg := miniTickerMsg("BTCUSDT", "60123.45", "61000.00", "59500.00", "12345.67")
	raw, _ := json.Marshal(msg)

	if err := feed.handleMessage(context.Background(), raw); err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}

	events := bus.published()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Type != event.TickerUpdated {
		t.Errorf("expected TickerUpdated, got %v", e.Type)
	}
	if e.StockCode != "BTCUSDT" {
		t.Errorf("expected BTCUSDT, got %q", e.StockCode)
	}
}

func TestHandleMessage_UnknownSymbol(t *testing.T) {
	t.Parallel()
	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := BinanceConfig{
		Symbols: map[string]string{"btcusdt": "BTCUSDT"},
	}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	// XYZUSDT is not in the symbol map — should be silently ignored.
	msg := miniTickerMsg("XYZUSDT", "1.00", "1.10", "0.90", "999")
	raw, _ := json.Marshal(msg)

	if err := feed.handleMessage(context.Background(), raw); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bus.published()) != 0 {
		t.Errorf("expected no events for unknown symbol, got %d", len(bus.published()))
	}
}

func TestHandleMessage_StockMapping(t *testing.T) {
	t.Parallel()
	bus := newStubBus()
	repo := newStubMarketRepo()
	// Map five standard symbols.
	symbols := map[string]string{
		"btcusdt": "BTCUSDT",
		"ethusdt": "ETHUSDT",
		"bnbusdt": "BNBUSDT",
		"solusdt": "SOLUSDT",
		"adausdt": "ADAUSDT",
	}
	cfg := BinanceConfig{Symbols: symbols}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	for binanceSym, localCode := range symbols {
		raw, _ := json.Marshal(miniTickerMsg(binanceSym, "100.00", "110.00", "90.00", "500"))
		if err := feed.handleMessage(context.Background(), raw); err != nil {
			t.Fatalf("handleMessage(%s) error: %v", binanceSym, err)
		}

		events := bus.published()
		last := events[len(events)-1]
		if last.StockCode != localCode {
			t.Errorf("symbol %s: expected stock code %q, got %q", binanceSym, localCode, last.StockCode)
		}
	}
}

func TestHandleMessage_PriceFields(t *testing.T) {
	t.Parallel()
	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := BinanceConfig{Symbols: map[string]string{"ethusdt": "ETHUSDT"}}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	raw, _ := json.Marshal(miniTickerMsg("ETHUSDT", "3000.50", "3100.00", "2900.00", "5000.5"))
	if err := feed.handleMessage(context.Background(), raw); err != nil {
		t.Fatal(err)
	}

	tk := repo.latest("ETHUSDT")
	if tk == nil {
		t.Fatal("no ticker in repo")
	}
	// 3000.50 * 100 = 300050
	if tk.LastPrice != 300050 {
		t.Errorf("expected LastPrice 300050, got %d", tk.LastPrice)
	}
	// 3100.00 * 100 = 310000
	if tk.High != 310000 {
		t.Errorf("expected High 310000, got %d", tk.High)
	}
	// 2900.00 * 100 = 290000
	if tk.Low != 290000 {
		t.Errorf("expected Low 290000, got %d", tk.Low)
	}
	// volume truncated to int: 5000
	if tk.Volume != 5000 {
		t.Errorf("expected Volume 5000, got %d", tk.Volume)
	}
}

func TestHandleMessage_InvalidJSON(t *testing.T) {
	t.Parallel()
	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := BinanceConfig{Symbols: map[string]string{"btcusdt": "BTCUSDT"}}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	err := feed.handleMessage(context.Background(), []byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHandleMessage_InvalidPrice(t *testing.T) {
	t.Parallel()
	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := BinanceConfig{Symbols: map[string]string{"btcusdt": "BTCUSDT"}}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	msg := miniTickerMsg("BTCUSDT", "not-a-price", "61000.00", "59500.00", "100")
	raw, _ := json.Marshal(msg)

	err := feed.handleMessage(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error for invalid price")
	}
}

// ── buildStreams unit test ────────────────────────────────────────────────────

func TestBuildStreams(t *testing.T) {
	t.Parallel()
	cfg := BinanceConfig{
		Symbols: map[string]string{
			"btcusdt": "BTCUSDT",
			"ethusdt": "ETHUSDT",
		},
	}
	feed := NewBinanceFeed(cfg, newStubBus(), newStubMarketRepo(), nil)
	streams := feed.buildStreams()
	if len(streams) != 2 {
		t.Fatalf("expected 2 streams, got %d", len(streams))
	}
	for _, s := range streams {
		if !strings.HasSuffix(s, "@miniTicker") {
			t.Errorf("stream %q missing @miniTicker suffix", s)
		}
	}
}

// ── mock WebSocket server tests ───────────────────────────────────────────────

// mockBinanceServer creates a test HTTP server that mimics the Binance combined
// stream endpoint. It sends the given messages then closes the connection.
func mockBinanceServer(t *testing.T, messages []binanceStreamMsg, closeCode websocket.StatusCode) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(closeCode, "") }()

		for _, msg := range messages {
			if err := wsjson.Write(r.Context(), conn, msg); err != nil {
				return
			}
		}
		// Hold connection open briefly so the client receives all messages.
		time.Sleep(50 * time.Millisecond)
	}))
	return srv
}

// wsURL converts an http:// test server URL to ws://.
func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestBinanceFeed_ReceivesMessages(t *testing.T) {
	t.Parallel()

	msgs := []binanceStreamMsg{
		miniTickerMsg("BTCUSDT", "60000.00", "61000.00", "59000.00", "100"),
		miniTickerMsg("ETHUSDT", "3000.00", "3100.00", "2900.00", "200"),
	}
	srv := mockBinanceServer(t, msgs, websocket.StatusNormalClosure)
	defer srv.Close()

	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := BinanceConfig{
		WSURL: wsURL(srv),
		Symbols: map[string]string{
			"btcusdt": "BTCUSDT",
			"ethusdt": "ETHUSDT",
		},
		MaxBackoff: 100 * time.Millisecond,
	}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Start blocks until ctx cancelled or permanent disconnect.
	_ = feed.Start(ctx)

	events := bus.published()
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}
}

func TestBinanceFeed_StockCodeMapped(t *testing.T) {
	t.Parallel()

	msgs := []binanceStreamMsg{
		miniTickerMsg("BTCUSDT", "60000.00", "61000.00", "59000.00", "100"),
	}
	srv := mockBinanceServer(t, msgs, websocket.StatusNormalClosure)
	defer srv.Close()

	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := BinanceConfig{
		WSURL:      wsURL(srv),
		Symbols:    map[string]string{"btcusdt": "BTCUSDT"},
		MaxBackoff: 100 * time.Millisecond,
	}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = feed.Start(ctx)

	events := bus.published()
	found := false
	for _, e := range events {
		if e.StockCode == "BTCUSDT" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TickerUpdated event with StockCode=BTCUSDT")
	}
}

func TestBinanceFeed_ReconnectsOnDisconnect(t *testing.T) {
	t.Parallel()

	// Count connections: after the first one closes immediately,
	// the feed should reconnect at least once.
	var connCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&connCount, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		if n == 1 {
			// First connection: close immediately to trigger reconnect.
			_ = conn.Close(websocket.StatusInternalError, "simulated error")
			return
		}
		// Second connection: send one message, then close normally.
		msg := miniTickerMsg("BTCUSDT", "60000.00", "61000.00", "59000.00", "100")
		_ = wsjson.Write(r.Context(), conn, msg)
		time.Sleep(50 * time.Millisecond)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()

	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := BinanceConfig{
		WSURL:      wsURL(srv),
		Symbols:    map[string]string{"btcusdt": "BTCUSDT"},
		MaxBackoff: 50 * time.Millisecond, // short backoff for tests
	}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = feed.Start(ctx)

	if atomic.LoadInt32(&connCount) < 2 {
		t.Errorf("expected at least 2 connections (reconnect), got %d", atomic.LoadInt32(&connCount))
	}
}

func TestBinanceFeed_StopViaContextCancel(t *testing.T) {
	t.Parallel()

	// Server that holds the connection open indefinitely.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
		// Block until the client disconnects.
		_, _, _ = conn.Read(r.Context())
	}))
	defer srv.Close()

	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := BinanceConfig{
		WSURL:      wsURL(srv),
		Symbols:    map[string]string{"btcusdt": "BTCUSDT"},
		MaxBackoff: 30 * time.Second,
	}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- feed.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("BinanceFeed did not stop within 2s after context cancel")
	}
}

func TestBinanceFeed_NoSymbols(t *testing.T) {
	t.Parallel()
	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := BinanceConfig{
		WSURL:      "ws://localhost:9999",
		Symbols:    map[string]string{},
		MaxBackoff: 50 * time.Millisecond,
	}
	feed := NewBinanceFeed(cfg, bus, repo, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Should return quickly with an error (no symbols) but not hang.
	done := make(chan error, 1)
	go func() { done <- feed.Start(ctx) }()

	select {
	case <-done:
		// OK — may return nil (ctx expired) or nil (loop exited on ctx)
	case <-time.After(2 * time.Second):
		t.Fatal("feed did not stop within 2s")
	}
}

// ── DefaultBinanceConfig ──────────────────────────────────────────────────────

func TestDefaultBinanceConfig(t *testing.T) {
	t.Parallel()
	cfg := DefaultBinanceConfig()
	if cfg.WSURL == "" {
		t.Error("WSURL should not be empty")
	}
	if len(cfg.Symbols) == 0 {
		t.Error("Symbols should not be empty")
	}
	requiredSymbols := []string{"btcusdt", "ethusdt", "bnbusdt", "solusdt", "adausdt"}
	for _, sym := range requiredSymbols {
		if _, ok := cfg.Symbols[sym]; !ok {
			t.Errorf("missing symbol %q in default config", sym)
		}
	}
	if cfg.MaxBackoff <= 0 {
		t.Error("MaxBackoff should be positive")
	}
}

// ── suppress unused import ────────────────────────────────────────────────────

var _ = fmt.Sprintf
