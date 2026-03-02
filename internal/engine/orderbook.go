// Package engine implements the core order-matching logic for the mini exchange.
//
// Concurrency model:
//   - Each stock has exactly ONE Matcher goroutine.
//   - The Matcher is the sole reader/writer of its local orderBook.
//   - No mutexes are needed inside the orderBook because it is only ever
//     accessed from that single goroutine (channel-based isolation).
//
// This file contains the per-stock in-memory order book.
package engine

import (
	"sort"

	"github.com/izzam/mini-exchange/internal/domain/entity"
)

// orderEntry is a single resting order in the book, augmented with the
// remaining unmatched quantity so we don't mutate the original entity.Order
// directly until a trade is confirmed.
type orderEntry struct {
	order     *entity.Order
	remaining int64 // unmatched quantity; starts equal to order.Quantity
}

// orderBook maintains the bid and ask sides for a single stock.
//
// Bids:  sorted DESC by price, then ASC by insertion-time (FIFO at same price).
// Asks:  sorted ASC  by price, then ASC by insertion-time (FIFO at same price).
//
// NOT goroutine-safe — must only be accessed from a single Matcher goroutine.
type orderBook struct {
	stockCode string
	bids      []*orderEntry // highest bid first
	asks      []*orderEntry // lowest ask first
}

func newOrderBook(stockCode string) *orderBook {
	return &orderBook{stockCode: stockCode}
}

// addOrder inserts a resting order into the correct side, maintaining sort order.
func (ob *orderBook) addOrder(o *entity.Order) {
	entry := &orderEntry{order: o, remaining: o.RemainingQuantity()}

	switch o.Side {
	case entity.SideBuy:
		ob.bids = append(ob.bids, entry)
		// Sort DESC by price; stable preserves insertion order (FIFO) at same price.
		sort.SliceStable(ob.bids, func(i, j int) bool {
			return ob.bids[i].order.Price > ob.bids[j].order.Price
		})
	case entity.SideSell:
		ob.asks = append(ob.asks, entry)
		// Sort ASC by price.
		sort.SliceStable(ob.asks, func(i, j int) bool {
			return ob.asks[i].order.Price < ob.asks[j].order.Price
		})
	}
}

// removeOrder deletes a fully-filled or cancelled order from the book.
func (ob *orderBook) removeOrder(orderID string) {
	ob.bids = removeEntry(ob.bids, orderID)
	ob.asks = removeEntry(ob.asks, orderID)
}

func removeEntry(entries []*orderEntry, id string) []*orderEntry {
	for i, e := range entries {
		if e.order.ID == id {
			return append(entries[:i], entries[i+1:]...)
		}
	}
	return entries
}

// bestBid returns the top-of-book bid entry (highest price), or nil.
func (ob *orderBook) bestBid() *orderEntry {
	if len(ob.bids) == 0 {
		return nil
	}
	return ob.bids[0]
}

// bestAsk returns the top-of-book ask entry (lowest price), or nil.
func (ob *orderBook) bestAsk() *orderEntry {
	if len(ob.asks) == 0 {
		return nil
	}
	return ob.asks[0]
}

// getDepth builds an entity.OrderBook snapshot with at most `levels` price
// levels on each side. It aggregates quantity across orders at the same price.
func (ob *orderBook) getDepth(levels int) *entity.OrderBook {
	return &entity.OrderBook{
		StockCode: ob.stockCode,
		Bids:      aggregateLevels(ob.bids, levels),
		Asks:      aggregateLevels(ob.asks, levels),
	}
}

// aggregateLevels collapses individual order entries into PriceLevel aggregates.
// The input slice must already be in the correct sort order.
func aggregateLevels(entries []*orderEntry, maxLevels int) []entity.PriceLevel {
	var levels []entity.PriceLevel

	for _, e := range entries {
		if e.remaining <= 0 {
			continue
		}
		if len(levels) > 0 && levels[len(levels)-1].Price == e.order.Price {
			// Same price level — aggregate.
			levels[len(levels)-1].Quantity += e.remaining
			levels[len(levels)-1].Count++
		} else {
			if maxLevels > 0 && len(levels) >= maxLevels {
				break
			}
			levels = append(levels, entity.PriceLevel{
				Price:    e.order.Price,
				Quantity: e.remaining,
				Count:    1,
			})
		}
	}
	return levels
}
