package memory

import (
	"context"
	"sync"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
)

// TradeRepo is a thread-safe in-memory implementation of repository.TradeRepository.
//
// Storage layout:
//   - byID     – O(1) lookup by trade ID
//   - byStock  – per-stock insertion-ordered slice for recent-trade queries
//   - all      – global insertion-ordered slice for FindAll pagination
type TradeRepo struct {
	mu      sync.RWMutex
	byID    map[string]*entity.Trade
	byStock map[string][]*entity.Trade
	all     []*entity.Trade
}

// NewTradeRepo constructs an empty TradeRepo.
func NewTradeRepo() *TradeRepo {
	return &TradeRepo{
		byID:    make(map[string]*entity.Trade),
		byStock: make(map[string][]*entity.Trade),
		all:     make([]*entity.Trade, 0, 256),
	}
}

// Save inserts a new trade. Returns repository.ErrDuplicate if the ID already exists.
func (r *TradeRepo) Save(_ context.Context, trade *entity.Trade) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byID[trade.ID]; exists {
		return repository.ErrDuplicate
	}

	cp := copyTrade(trade)
	r.byID[cp.ID] = cp
	r.byStock[cp.StockCode] = append(r.byStock[cp.StockCode], cp)
	r.all = append(r.all, cp)
	return nil
}

// FindByStockCode returns up to limit of the most recent trades for a stock.
// Trades are returned in reverse-insertion order (newest first).
func (r *TradeRepo) FindByStockCode(_ context.Context, stockCode string, limit int) ([]*entity.Trade, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	src := r.byStock[stockCode]
	if len(src) == 0 {
		return []*entity.Trade{}, nil
	}

	// Return newest first: iterate from the end of the slice.
	n := len(src)
	if limit > 0 && limit < n {
		n = limit
	}

	result := make([]*entity.Trade, n)
	for i := 0; i < n; i++ {
		result[i] = copyTrade(src[len(src)-1-i])
	}
	return result, nil
}

// FindAll returns trades matching the filter with pagination.
// The second return value is the total matching count before pagination.
func (r *TradeRepo) FindAll(_ context.Context, filter entity.TradeFilter) ([]*entity.Trade, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matched []*entity.Trade
	for _, t := range r.all {
		if filter.StockCode != "" && t.StockCode != filter.StockCode {
			continue
		}
		if filter.UserID != "" && t.BuyerUserID != filter.UserID && t.SellerUserID != filter.UserID {
			continue
		}
		matched = append(matched, t)
	}

	total := int64(len(matched))

	page := filter.Page
	perPage := filter.PerPage
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 20
	}

	start := (page - 1) * perPage
	if start >= len(matched) {
		return []*entity.Trade{}, total, nil
	}
	end := start + perPage
	if end > len(matched) {
		end = len(matched)
	}

	result := make([]*entity.Trade, end-start)
	for i, t := range matched[start:end] {
		result[i] = copyTrade(t)
	}
	return result, total, nil
}

// copyTrade returns a shallow copy of a trade (all fields are value types).
func copyTrade(t *entity.Trade) *entity.Trade {
	cp := *t
	return &cp
}
