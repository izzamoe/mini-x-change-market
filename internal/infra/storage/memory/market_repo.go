package memory

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

// tickerEntry wraps an *entity.Ticker for atomic pointer swaps.
// We store an unsafe.Pointer so we can use atomic.LoadPointer /
// atomic.StorePointer without a lock, giving lock-free reads on
// the hot path (ticker is read on every WebSocket tick).
type tickerEntry struct {
	ptr unsafe.Pointer // *entity.Ticker
}

func (e *tickerEntry) load() *entity.Ticker {
	p := atomic.LoadPointer(&e.ptr)
	if p == nil {
		return nil
	}
	return (*entity.Ticker)(p)
}

func (e *tickerEntry) store(t *entity.Ticker) {
	atomic.StorePointer(&e.ptr, unsafe.Pointer(t))
}

// MarketRepo is a thread-safe in-memory implementation of repository.MarketRepository.
//
// Design choices:
//   - Tickers are accessed via atomic pointer swaps (lock-free reads, ideal for
//     high-frequency WebSocket broadcasting).
//   - Order books use sync.RWMutex (writes are less frequent; slices require
//     copying to be safe, which precludes a simple atomic swap).
type MarketRepo struct {
	mu      sync.RWMutex
	tickers map[string]*tickerEntry // stock code → atomic ticker
	books   map[string]*entity.OrderBook
}

// NewMarketRepo constructs an empty MarketRepo.
func NewMarketRepo() *MarketRepo {
	return &MarketRepo{
		tickers: make(map[string]*tickerEntry),
		books:   make(map[string]*entity.OrderBook),
	}
}

// InitStock creates the initial (empty) ticker and order book for a stock.
// Safe to call multiple times; subsequent calls for an existing stock are no-ops.
func (r *MarketRepo) InitStock(_ context.Context, stock entity.Stock) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tickers[stock.Code]; !exists {
		entry := &tickerEntry{}
		t := &entity.Ticker{
			StockCode: stock.Code,
			UpdatedAt: time.Now(),
		}
		entry.store(t)
		r.tickers[stock.Code] = entry
	}

	if _, exists := r.books[stock.Code]; !exists {
		r.books[stock.Code] = &entity.OrderBook{
			StockCode: stock.Code,
			Bids:      []entity.PriceLevel{},
			Asks:      []entity.PriceLevel{},
			UpdatedAt: time.Now(),
		}
	}
	return nil
}

// GetTicker returns the current ticker snapshot for a stock.
// The read is lock-free (atomic pointer load).
func (r *MarketRepo) GetTicker(_ context.Context, stockCode string) (*entity.Ticker, error) {
	// We need the read lock only to check map membership (map reads are not
	// goroutine-safe in Go without synchronisation).
	r.mu.RLock()
	entry, ok := r.tickers[stockCode]
	r.mu.RUnlock()

	if !ok {
		return nil, repository.ErrNotFound
	}

	t := entry.load()
	if t == nil {
		return nil, repository.ErrNotFound
	}
	cp := copyTicker(t)
	return cp, nil
}

// GetAllTickers returns the current snapshot for every registered stock.
func (r *MarketRepo) GetAllTickers(_ context.Context) ([]*entity.Ticker, error) {
	r.mu.RLock()
	codes := make([]string, 0, len(r.tickers))
	entries := make([]*tickerEntry, 0, len(r.tickers))
	for code, entry := range r.tickers {
		codes = append(codes, code)
		entries = append(entries, entry)
	}
	r.mu.RUnlock()

	result := make([]*entity.Ticker, 0, len(codes))
	for _, entry := range entries {
		if t := entry.load(); t != nil {
			result = append(result, copyTicker(t))
		}
	}
	return result, nil
}

// UpdateTickerFromTrade performs an atomic read-modify-write using the write
// lock, so the matching engine's mutation cannot race with a simulator update.
// It applies trade price/qty via entity.Ticker.UpdateFromTrade and returns a
// copy of the updated ticker.
func (r *MarketRepo) UpdateTickerFromTrade(_ context.Context, stockCode string, price, qty int64) (*entity.Ticker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.tickers[stockCode]
	if !ok {
		return nil, repository.ErrNotFound
	}

	// Load current snapshot, mutate, and store — all under the write lock.
	current := entry.load()
	if current == nil {
		return nil, repository.ErrNotFound
	}
	updated := copyTicker(current)
	updated.UpdateFromTrade(price, qty)
	entry.store(updated)
	return copyTicker(updated), nil
}

// UpdateTicker atomically replaces the ticker snapshot for a stock.
func (r *MarketRepo) UpdateTicker(_ context.Context, ticker *entity.Ticker) error {
	r.mu.RLock()
	entry, ok := r.tickers[ticker.StockCode]
	r.mu.RUnlock()

	if !ok {
		return repository.ErrNotFound
	}

	cp := copyTicker(ticker)
	entry.store(cp)
	return nil
}

// GetOrderBook returns the current order book snapshot for a stock.
func (r *MarketRepo) GetOrderBook(_ context.Context, stockCode string) (*entity.OrderBook, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	book, ok := r.books[stockCode]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return copyOrderBook(book), nil
}

// UpdateOrderBook atomically replaces the order book snapshot for a stock.
func (r *MarketRepo) UpdateOrderBook(_ context.Context, book *entity.OrderBook) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.books[book.StockCode]; !ok {
		return repository.ErrNotFound
	}
	r.books[book.StockCode] = copyOrderBook(book)
	return nil
}

// copyTicker returns a deep copy of a Ticker.
func copyTicker(t *entity.Ticker) *entity.Ticker {
	cp := *t
	return &cp
}

// copyOrderBook returns a deep copy of an OrderBook, including its slice data.
func copyOrderBook(ob *entity.OrderBook) *entity.OrderBook {
	cp := &entity.OrderBook{
		StockCode: ob.StockCode,
		UpdatedAt: ob.UpdatedAt,
		Bids:      make([]entity.PriceLevel, len(ob.Bids)),
		Asks:      make([]entity.PriceLevel, len(ob.Asks)),
	}
	copy(cp.Bids, ob.Bids)
	copy(cp.Asks, ob.Asks)
	return cp
}
