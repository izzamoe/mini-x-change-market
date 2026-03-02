package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/event"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

// BinanceConfig holds connection settings for the Binance public market feed.
type BinanceConfig struct {
	// WSURL is the Binance WebSocket base URL.
	// Default: wss://stream.binance.com:9443
	WSURL string

	// Symbols maps Binance stream symbols (lowercase) to local stock codes.
	// e.g. "btcusdt" → "BTCUSDT"
	Symbols map[string]string

	// MaxBackoff caps the reconnect delay.
	MaxBackoff time.Duration
}

// DefaultBinanceConfig returns sensible defaults for the five major crypto pairs.
func DefaultBinanceConfig() BinanceConfig {
	return BinanceConfig{
		WSURL: "wss://stream.binance.com:9443",
		Symbols: map[string]string{
			"btcusdt": "BTCUSDT",
			"ethusdt": "ETHUSDT",
			"bnbusdt": "BNBUSDT",
			"solusdt": "SOLUSDT",
			"adausdt": "ADAUSDT",
		},
		MaxBackoff: 30 * time.Second,
	}
}

// binanceMiniTicker is the JSON shape of a Binance 24hrMiniTicker event.
type binanceMiniTicker struct {
	EventType string `json:"e"`
	Symbol    string `json:"s"`
	Close     string `json:"c"` // last price
	High      string `json:"h"`
	Low       string `json:"l"`
	Volume    string `json:"v"`
}

// binanceStreamMsg wraps the combined-stream envelope.
type binanceStreamMsg struct {
	Stream string            `json:"stream"`
	Data   binanceMiniTicker `json:"data"`
}

// BinanceFeed connects to the Binance public WebSocket mini-ticker stream and
// forwards price updates into the exchange event bus.
type BinanceFeed struct {
	cfg        BinanceConfig
	eventBus   event.Bus
	marketRepo repository.MarketRepository
	stocks     []entity.Stock // stocks to initialize

	cancel context.CancelFunc
}

// NewBinanceFeed creates a BinanceFeed. stocks is the list of Binance-sourced
// stocks to initialise in the market repo before streaming begins.
func NewBinanceFeed(cfg BinanceConfig, bus event.Bus, repo repository.MarketRepository, stocks []entity.Stock) *BinanceFeed {
	return &BinanceFeed{
		cfg:        cfg,
		eventBus:   bus,
		marketRepo: repo,
		stocks:     stocks,
	}
}

// Start connects to Binance and streams price updates until ctx is cancelled.
// On disconnection it reconnects with exponential backoff.
func (b *BinanceFeed) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	defer cancel()

	// Initialise tickers in the market repo so they show up in snapshots
	// even before the first Binance message arrives.
	for _, stock := range b.stocks {
		if err := b.marketRepo.InitStock(ctx, stock); err != nil {
			slog.Warn("binance feed: failed to init stock", "code", stock.Code, "err", err)
		}
	}
	slog.Info("binance feed: stocks initialised", "count", len(b.stocks))

	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		err := b.connect(ctx)
		if err == nil || ctx.Err() != nil {
			return nil
		}

		backoff := calcBackoff(attempt, b.cfg.MaxBackoff)
		slog.Error("binance feed disconnected, reconnecting",
			"attempt", attempt, "backoff", backoff, "err", err)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		attempt++
	}
}

// Stop cancels the feed context.
func (b *BinanceFeed) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
}

// connect dials Binance, reads until the context is cancelled or an error occurs.
func (b *BinanceFeed) connect(ctx context.Context) error {
	streams := b.buildStreams()
	if len(streams) == 0 {
		return fmt.Errorf("no symbols configured")
	}
	url := b.cfg.WSURL + "/stream?streams=" + strings.Join(streams, "/")

	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	slog.Info("binance feed connected", "streams", len(streams))

	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if err := b.handleMessage(ctx, raw); err != nil {
			slog.Warn("binance: message handling error", "err", err)
		}
	}
}

// handleMessage parses one Binance combined-stream message and emits a
// TickerUpdated event if the symbol is in the configured map.
func (b *BinanceFeed) handleMessage(ctx context.Context, raw []byte) error {
	var msg binanceStreamMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	symbol := strings.ToLower(msg.Data.Symbol)
	stockCode, ok := b.cfg.Symbols[symbol]
	if !ok {
		return nil // not a symbol we care about
	}

	lastPrice, err := parsePrice(msg.Data.Close)
	if err != nil {
		return err
	}
	high, _ := parsePrice(msg.Data.High)
	low, _ := parsePrice(msg.Data.Low)
	vol, _ := parseVolume(msg.Data.Volume)

	ticker := &entity.Ticker{
		StockCode: stockCode,
		LastPrice: lastPrice,
		High:      high,
		Low:       low,
		Volume:    vol,
		UpdatedAt: time.Now(),
	}

	if err := b.marketRepo.UpdateTicker(ctx, ticker); err != nil {
		slog.Debug("binance: UpdateTicker error", "stock", stockCode, "err", err)
	}

	b.eventBus.Publish(event.Event{
		Type:      event.TickerUpdated,
		StockCode: stockCode,
		Payload:   ticker,
		Timestamp: time.Now(),
	})
	return nil
}

// buildStreams returns the list of Binance stream names (symbol@miniTicker).
func (b *BinanceFeed) buildStreams() []string {
	streams := make([]string, 0, len(b.cfg.Symbols))
	for sym := range b.cfg.Symbols {
		streams = append(streams, sym+"@miniTicker")
	}
	return streams
}

// ── helpers ───────────────────────────────────────────────────────────────────

// parsePrice converts a Binance decimal string (e.g. "60123.45") to an integer
// in the smallest unit by multiplying by 100 (cents equivalent).
func parsePrice(s string) (int64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parsePrice %q: %w", s, err)
	}
	return int64(math.Round(f * 100)), nil
}

// parseVolume converts a Binance volume string to integer units.
func parseVolume(s string) (int64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parseVolume %q: %w", s, err)
	}
	return int64(f), nil
}

// calcBackoff returns the exponential backoff duration for a given attempt,
// capped at maxBackoff: 1s, 2s, 4s, 8s … maxBackoff.
func calcBackoff(attempt int, maxBackoff time.Duration) time.Duration {
	d := time.Duration(1<<uint(attempt)) * time.Second
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}
