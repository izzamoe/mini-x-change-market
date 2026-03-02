package entity

import "time"

// PriceLevel represents aggregated quantity at a specific price in the order book.
type PriceLevel struct {
	Price    int64 `json:"price"`
	Quantity int64 `json:"quantity"` // total quantity at this price
	Count    int   `json:"count"`    // number of orders at this price
}

// OrderBook holds the current state of bids and asks for a stock.
// Bids are sorted descending by price (highest first).
// Asks are sorted ascending by price (lowest first).
type OrderBook struct {
	StockCode string       `json:"stock_code"`
	Bids      []PriceLevel `json:"bids"` // sorted DESC
	Asks      []PriceLevel `json:"asks"` // sorted ASC
	UpdatedAt time.Time    `json:"updated_at"`
}

// BestBid returns the highest bid price level, or nil if no bids.
func (ob *OrderBook) BestBid() *PriceLevel {
	if len(ob.Bids) == 0 {
		return nil
	}
	return &ob.Bids[0]
}

// BestAsk returns the lowest ask price level, or nil if no asks.
func (ob *OrderBook) BestAsk() *PriceLevel {
	if len(ob.Asks) == 0 {
		return nil
	}
	return &ob.Asks[0]
}
