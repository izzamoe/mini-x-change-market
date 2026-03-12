package simulator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/event"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

// ── stub implementations ──────────────────────────────────────────────────────

// stubBus is a minimal in-process event bus for tests.
type stubBus struct {
	mu     sync.Mutex
	events []event.Event
	subs   map[event.Type][]event.HandlerFunc
	nextID int
	subIDs map[string]event.Type
}

func newStubBus() *stubBus {
	return &stubBus{
		subs:   make(map[event.Type][]event.HandlerFunc),
		subIDs: make(map[string]event.Type),
	}
}

func (b *stubBus) Publish(e event.Event) {
	b.mu.Lock()
	b.events = append(b.events, e)
	handlers := make([]event.HandlerFunc, len(b.subs[e.Type]))
	copy(handlers, b.subs[e.Type])
	b.mu.Unlock()
	for _, h := range handlers {
		h(e)
	}
}

func (b *stubBus) Subscribe(t event.Type, fn event.HandlerFunc) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := "sub-" + string(rune('0'+b.nextID))
	b.subs[t] = append(b.subs[t], fn)
	b.subIDs[id] = t
	return id
}

func (b *stubBus) Unsubscribe(_ string) {}
func (b *stubBus) Start()               {}
func (b *stubBus) Stop()                {}

func (b *stubBus) published() []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]event.Event, len(b.events))
	copy(cp, b.events)
	return cp
}

// stubMarketRepo records the latest ticker per stock code.
type stubMarketRepo struct {
	mu        sync.Mutex
	tickers   map[string]*entity.Ticker
	initErr   error
	updateErr error
}

func newStubMarketRepo() *stubMarketRepo {
	return &stubMarketRepo{tickers: make(map[string]*entity.Ticker)}
}

func (r *stubMarketRepo) InitStock(_ context.Context, s entity.Stock) error {
	if r.initErr != nil {
		return r.initErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tickers[s.Code] = &entity.Ticker{StockCode: s.Code}
	return nil
}

func (r *stubMarketRepo) UpdateTicker(_ context.Context, t *entity.Ticker) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *t
	r.tickers[t.StockCode] = &cp
	return nil
}

func (r *stubMarketRepo) GetTicker(_ context.Context, code string) (*entity.Ticker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tickers[code]
	if !ok {
		return nil, repository.ErrNotFound
	}
	cp := *t
	return &cp, nil
}

func (r *stubMarketRepo) GetAllTickers(_ context.Context) ([]*entity.Ticker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*entity.Ticker, 0, len(r.tickers))
	for _, t := range r.tickers {
		cp := *t
		out = append(out, &cp)
	}
	return out, nil
}

func (r *stubMarketRepo) GetOrderBook(_ context.Context, _ string) (*entity.OrderBook, error) {
	return nil, repository.ErrNotFound
}

func (r *stubMarketRepo) UpdateOrderBook(_ context.Context, _ *entity.OrderBook) error {
	return nil
}

func (r *stubMarketRepo) UpdateTickerFromTrade(_ context.Context, stockCode string, price, qty int64) (*entity.Ticker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tickers[stockCode]
	if !ok {
		return nil, repository.ErrNotFound
	}
	cp := *t
	cp.UpdateFromTrade(price, qty)
	r.tickers[stockCode] = &cp
	result := cp
	return &result, nil
}

func (r *stubMarketRepo) latest(code string) *entity.Ticker {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tickers[code]
}

// ── helpers ───────────────────────────────────────────────────────────────────

func testStock() entity.Stock {
	return entity.Stock{Code: "TEST", Name: "Test Stock", BasePrice: 1000, TickSize: 10}
}

// ── unit tests for pure price math ───────────────────────────────────────────

func TestNextPrice_StaysWithinBounds(t *testing.T) {
	t.Parallel()
	stock := testStock()
	min := stock.BasePrice * 90 / 100
	max := stock.BasePrice * 110 / 100

	for i := 0; i < 10_000; i++ {
		price := nextPrice(stock.BasePrice, stock.BasePrice, stock.TickSize)
		if price < min || price > max {
			t.Fatalf("price %d out of [%d, %d]", price, min, max)
		}
	}
}

func TestNextPrice_RespectTickSize(t *testing.T) {
	t.Parallel()
	stock := testStock() // TickSize = 10
	for i := 0; i < 1_000; i++ {
		price := nextPrice(stock.BasePrice, stock.BasePrice, stock.TickSize)
		if price%stock.TickSize != 0 {
			t.Fatalf("price %d not divisible by tick size %d", price, stock.TickSize)
		}
	}
}

func TestNextPrice_NeverBelowTickSize(t *testing.T) {
	t.Parallel()
	// Edge case: base price is 1 tick, should never go below 1 tick.
	stock := entity.Stock{Code: "X", BasePrice: 10, TickSize: 10}
	for i := 0; i < 1_000; i++ {
		price := nextPrice(stock.BasePrice, stock.BasePrice, stock.TickSize)
		if price < stock.TickSize {
			t.Fatalf("price %d < tick size %d", price, stock.TickSize)
		}
	}
}

func TestClamp(t *testing.T) {
	t.Parallel()
	if clamp(5, 10, 20) != 10 {
		t.Fatal("expected 10")
	}
	if clamp(25, 10, 20) != 20 {
		t.Fatal("expected 20")
	}
	if clamp(15, 10, 20) != 15 {
		t.Fatal("expected 15")
	}
}

func TestRandomDuration(t *testing.T) {
	t.Parallel()
	for i := 0; i < 1_000; i++ {
		d := randomDuration(10*time.Millisecond, 50*time.Millisecond)
		if d < 10*time.Millisecond || d >= 50*time.Millisecond {
			t.Fatalf("randomDuration %v out of [10ms, 50ms)", d)
		}
	}
}

func TestRandomDuration_EqualMinMax(t *testing.T) {
	t.Parallel()
	d := randomDuration(5*time.Second, 5*time.Second)
	if d != 5*time.Second {
		t.Fatalf("expected 5s, got %v", d)
	}
}

// ── integration-style simulator tests ────────────────────────────────────────

// runSimulatorForTicks runs the simulator, collects n TickerUpdated events,
// then cancels the context (stopping the simulator).
func runSimulatorForTicks(t *testing.T, stock entity.Stock, n int) ([]*entity.Ticker, *stubMarketRepo) {
	t.Helper()

	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := SimulatorConfig{IntervalMin: 5 * time.Millisecond, IntervalMax: 10 * time.Millisecond}
	sim := NewSimulator([]entity.Stock{stock}, bus, repo, cfg)

	collected := make([]*entity.Ticker, 0, n)
	var mu sync.Mutex
	done := make(chan struct{})

	bus.Subscribe(event.TickerUpdated, func(e event.Event) {
		ticker, ok := e.Payload.(*entity.Ticker)
		if !ok {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if len(collected) < n {
			collected = append(collected, ticker)
			if len(collected) == n {
				close(done)
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		if err := sim.Start(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("simulator.Start returned unexpected error: %v", err)
		}
	}()

	select {
	case <-done:
		cancel() // stop simulator
	case <-ctx.Done():
		t.Fatalf("timed out waiting for %d ticker events", n)
	}

	// Give goroutine time to exit.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	return collected, repo
}

func TestSimulator_EmitsTickerEvents(t *testing.T) {
	t.Parallel()
	tickers, _ := runSimulatorForTicks(t, testStock(), 5)
	if len(tickers) < 5 {
		t.Fatalf("expected at least 5 tickers, got %d", len(tickers))
	}
	for _, tk := range tickers {
		if tk.StockCode != "TEST" {
			t.Errorf("unexpected stock code %q", tk.StockCode)
		}
	}
}

func TestSimulator_PriceWithinBounds(t *testing.T) {
	t.Parallel()
	stock := testStock()
	min := stock.BasePrice * 90 / 100
	max := stock.BasePrice * 110 / 100

	tickers, _ := runSimulatorForTicks(t, stock, 20)
	for _, tk := range tickers {
		if tk.LastPrice < min || tk.LastPrice > max {
			t.Errorf("price %d out of [%d, %d]", tk.LastPrice, min, max)
		}
	}
}

func TestSimulator_PriceRespectsTickSize(t *testing.T) {
	t.Parallel()
	stock := testStock() // TickSize = 10
	tickers, _ := runSimulatorForTicks(t, stock, 20)
	for _, tk := range tickers {
		if tk.LastPrice%stock.TickSize != 0 {
			t.Errorf("price %d not divisible by tick size %d", tk.LastPrice, stock.TickSize)
		}
	}
}

func TestSimulator_VolumeAccumulates(t *testing.T) {
	t.Parallel()
	tickers, _ := runSimulatorForTicks(t, testStock(), 5)
	for i := 1; i < len(tickers); i++ {
		if tickers[i].Volume < tickers[i-1].Volume {
			t.Errorf("volume decreased: tick[%d]=%d < tick[%d]=%d",
				i, tickers[i].Volume, i-1, tickers[i-1].Volume)
		}
	}
}

func TestSimulator_UpdatesMarketRepo(t *testing.T) {
	t.Parallel()
	_, repo := runSimulatorForTicks(t, testStock(), 3)
	tk := repo.latest("TEST")
	if tk == nil {
		t.Fatal("expected ticker in repo, got nil")
	}
	if tk.LastPrice <= 0 {
		t.Errorf("expected positive LastPrice, got %d", tk.LastPrice)
	}
}

func TestSimulator_HighLowTracked(t *testing.T) {
	t.Parallel()
	tickers, _ := runSimulatorForTicks(t, testStock(), 10)
	for _, tk := range tickers {
		if tk.High < tk.LastPrice {
			t.Errorf("high %d < last %d", tk.High, tk.LastPrice)
		}
		if tk.Low > tk.LastPrice {
			t.Errorf("low %d > last %d", tk.Low, tk.LastPrice)
		}
	}
}

func TestSimulator_TradeUpdateOverridesSimulation(t *testing.T) {
	t.Parallel()
	stock := testStock()
	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := SimulatorConfig{IntervalMin: 500 * time.Millisecond, IntervalMax: time.Second}
	sim := NewSimulator([]entity.Stock{stock}, bus, repo, cfg)

	var mu sync.Mutex
	var tradeBasedTicker *entity.Ticker
	gotTrade := make(chan struct{}, 1)

	// Collect only the ticker that follows the trade update.
	sendTrade := false
	bus.Subscribe(event.TickerUpdated, func(e event.Event) {
		mu.Lock()
		defer mu.Unlock()
		if sendTrade {
			if t2, ok := e.Payload.(*entity.Ticker); ok {
				tradeBasedTicker = t2
				select {
				case gotTrade <- struct{}{}:
				default:
				}
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = sim.Start(ctx)
	}()

	// Wait for the simulator to initialise (first tick may not come for 500ms).
	time.Sleep(30 * time.Millisecond)

	// Inject a trade at a specific price.
	tradePrice := int64(1200)
	mu.Lock()
	sendTrade = true
	mu.Unlock()

	bus.Publish(event.Event{
		Type:      event.TradeExecuted,
		StockCode: stock.Code,
		Payload:   &entity.Trade{StockCode: stock.Code, Price: tradePrice, Quantity: 100},
	})

	select {
	case <-gotTrade:
		mu.Lock()
		tk := tradeBasedTicker
		mu.Unlock()
		if tk == nil {
			t.Fatal("expected ticker after trade, got nil")
		}
		if tk.LastPrice != tradePrice {
			t.Errorf("expected last price %d after trade, got %d", tradePrice, tk.LastPrice)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for trade-based ticker event")
	}

	cancel()
}

func TestSimulator_StopGraceful(t *testing.T) {
	t.Parallel()
	stock := testStock()
	bus := newStubBus()
	repo := newStubMarketRepo()
	cfg := SimulatorConfig{IntervalMin: 10 * time.Millisecond, IntervalMax: 20 * time.Millisecond}
	sim := NewSimulator([]entity.Stock{stock}, bus, repo, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- sim.Start(ctx)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error from Start: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("simulator did not stop within 1s after context cancel")
	}
}
