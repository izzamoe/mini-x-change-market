package entity

import "time"

// Trade represents an executed match between a buy and a sell order.
type Trade struct {
	ID           string    `json:"id"`
	StockCode    string    `json:"stock_code"`
	BuyOrderID   string    `json:"buy_order_id"`
	SellOrderID  string    `json:"sell_order_id"`
	Price        int64     `json:"price"`    // execution price (passive order's price)
	Quantity     int64     `json:"quantity"` // matched quantity
	BuyerUserID  string    `json:"buyer_user_id"`
	SellerUserID string    `json:"seller_user_id"`
	ExecutedAt   time.Time `json:"executed_at"`
}

// TradeFilter holds filtering criteria for querying trade history.
type TradeFilter struct {
	StockCode string
	UserID    string // filter by buyer OR seller
	Page      int
	PerPage   int
}
