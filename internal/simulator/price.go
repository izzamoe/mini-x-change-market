package simulator

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/event"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

// SimulatorConfig controls the behaviour of the internal price simulator.
type SimulatorConfig struct {
	// IntervalMin / IntervalMax define the random tick period for each stock.
	IntervalMin time.Duration
	IntervalMax time.Duration
}

// DefaultSimulatorConfig returns sensible defaults (1–3 s tick period).
func DefaultSimulatorConfig() SimulatorConfig {
	return SimulatorConfig{
		IntervalMin: time.Second,
		IntervalMax: 3 * time.Second,
	}
}

// tradeUpdate carries the fill info from a real order-book match.
type tradeUpdate struct {
	price int64
	qty   int64
}

// Simulator runs one goroutine per stock, emitting realistic price ticks and
// incorporating real trade fills as they occur.
type Simulator struct {
	stocks     []entity.Stock
	eventBus   event.Bus
	marketRepo repository.MarketRepository
	cfg        SimulatorConfig

	// tradeChans delivers real-time fill prices to the per-stock goroutines.
	tradeChans map[string]chan tradeUpdate

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewSimulator creates a Simulator. Call Start to begin emitting price ticks.
func NewSimulator(
	stocks []entity.Stock,
	bus event.Bus,
	repo repository.MarketRepository,
	cfg SimulatorConfig,
) *Simulator {
	chans := make(map[string]chan tradeUpdate, len(stocks))
	for _, s := range stocks {
		chans[s.Code] = make(chan tradeUpdate, 16)
	}
	return &Simulator{
		stocks:     stocks,
		eventBus:   bus,
		marketRepo: repo,
		cfg:        cfg,
		tradeChans: chans,
	}
}

// Start initialises all stocks and launches a goroutine per stock.
// It also subscribes to TradeExecuted events so real fills update prices
// immediately rather than waiting for the next simulation tick.
// Start blocks until ctx is cancelled and all goroutines exit.
func (s *Simulator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	defer cancel()

	// Initialise tickers / order-books in the repo.
	for _, stock := range s.stocks {
		if err := s.marketRepo.InitStock(ctx, stock); err != nil {
			slog.Warn("simulator: failed to init stock", "code", stock.Code, "err", err)
		}
	}

	// Subscribe to trade fills so we can forward them to the right goroutine.
	subID := s.eventBus.Subscribe(event.TradeExecuted, func(e event.Event) {
		trade, ok := e.Payload.(*entity.Trade)
		if !ok {
			return
		}
		ch, exists := s.tradeChans[trade.StockCode]
		if !exists {
			return
		}
		select {
		case ch <- tradeUpdate{price: trade.Price, qty: trade.Quantity}:
		default: // drop if goroutine is busy; it will pick up the next tick
		}
	})
	defer s.eventBus.Unsubscribe(subID)

	// Launch one goroutine per stock.
	for _, stock := range s.stocks {
		s.wg.Add(1)
		go func(st entity.Stock) {
			defer s.wg.Done()
			s.simulateStock(ctx, st, s.tradeChans[st.Code])
		}(stock)
	}

	s.wg.Wait()
	return nil
}

// Stop cancels the simulation context and waits for all goroutines to exit.
func (s *Simulator) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// simulateStock is the per-stock goroutine. It emits a TickerUpdated event on
// every tick, using a random-walk with mean reversion algorithm, and honours
// real trade fills immediately.
func (s *Simulator) simulateStock(ctx context.Context, stock entity.Stock, tradeCh <-chan tradeUpdate) {
	currentPrice := stock.BasePrice
	prevClose := stock.BasePrice
	high := stock.BasePrice
	low := stock.BasePrice
	volume := int64(0)

	emit := func() {
		ticker := &entity.Ticker{
			StockCode: stock.Code,
			LastPrice: currentPrice,
			PrevClose: prevClose,
			Change:    currentPrice - prevClose,
			UpdatedAt: time.Now(),
		}
		if prevClose > 0 {
			ticker.ChangePct = float64(ticker.Change) / float64(prevClose) * 100
		}
		ticker.High = high
		ticker.Low = low
		ticker.Volume = volume

		if err := s.marketRepo.UpdateTicker(ctx, ticker); err != nil {
			slog.Debug("simulator: UpdateTicker error", "stock", stock.Code, "err", err)
		}
		s.eventBus.Publish(event.Event{
			Type:      event.TickerUpdated,
			StockCode: stock.Code,
			Payload:   ticker,
			Timestamp: time.Now(),
		})
	}

	for {
		delay := randomDuration(s.cfg.IntervalMin, s.cfg.IntervalMax)
		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			return

		case t := <-tradeCh:
			// Real trade fill: update price immediately without waiting for the tick.
			timer.Stop()
			tickVol := t.qty
			volume += tickVol
			currentPrice = t.price
			if currentPrice > high {
				high = currentPrice
			}
			if currentPrice < low || low == 0 {
				low = currentPrice
			}
			emit()

		case <-timer.C:
			// Simulation tick: random walk with mean reversion.
			newPrice := nextPrice(currentPrice, stock.BasePrice, stock.TickSize)
			tickVol := rand.Int63n(450) + 50 // 50–500 units per tick
			volume += tickVol
			currentPrice = newPrice
			if currentPrice > high {
				high = currentPrice
			}
			if currentPrice < low || low == 0 {
				low = currentPrice
			}
			emit()
		}
	}
}

// ── price math helpers ────────────────────────────────────────────────────────

// nextPrice applies one step of the random-walk / mean-reversion algorithm.
func nextPrice(current, base, tickSize int64) int64 {
	// Random walk component: ±0.5% of current price.
	delta := float64(current) * 0.005 * (rand.Float64()*2 - 1)
	// Mean reversion: pull 1% back toward base price each tick.
	delta += float64(base-current) * 0.01
	newPrice := current + int64(delta)
	// Round to tick size.
	newPrice = entity.RoundToTick(newPrice, tickSize)
	// Clamp to ±10% of base price.
	newPrice = clamp(newPrice, base*90/100, base*110/100)
	// Never go below tick size.
	if newPrice < tickSize {
		newPrice = tickSize
	}
	return newPrice
}

// randomDuration returns a uniform random duration in [min, max).
func randomDuration(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min)))
}

// clamp constrains v to [lo, hi].
func clamp(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
