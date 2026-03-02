package entity

import "time"

// Side represents the order side.
type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

// IsValid returns true if the side is a valid value.
func (s Side) IsValid() bool {
	return s == SideBuy || s == SideSell
}

// OrderStatus represents the lifecycle state of an order.
type OrderStatus string

const (
	OrderStatusOpen      OrderStatus = "OPEN"
	OrderStatusPartial   OrderStatus = "PARTIAL"
	OrderStatusFilled    OrderStatus = "FILLED"
	OrderStatusCancelled OrderStatus = "CANCELLED"
)

// IsValid returns true if the status is a valid value.
func (s OrderStatus) IsValid() bool {
	return s == OrderStatusOpen || s == OrderStatusPartial ||
		s == OrderStatusFilled || s == OrderStatusCancelled
}

// Order represents a trading order submitted by a user.
// Prices are stored as integers in the smallest currency unit (e.g., IDR cents)
// to avoid floating-point precision issues.
type Order struct {
	ID             string      `json:"id"`
	UserID         string      `json:"user_id"`
	StockCode      string      `json:"stock_code"`
	Side           Side        `json:"side"`
	Price          int64       `json:"price"`           // in smallest unit
	Quantity       int64       `json:"quantity"`        // total quantity requested
	FilledQuantity int64       `json:"filled_quantity"` // quantity matched so far
	Status         OrderStatus `json:"status"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// RemainingQuantity returns how much of the order is still unfilled.
func (o *Order) RemainingQuantity() int64 {
	return o.Quantity - o.FilledQuantity
}

// IsActive returns true if the order can still be matched.
func (o *Order) IsActive() bool {
	return o.Status == OrderStatusOpen || o.Status == OrderStatusPartial
}

// Fill updates the order after a partial or full fill.
// qty is the amount filled in this trade.
func (o *Order) Fill(qty int64) {
	o.FilledQuantity += qty
	o.UpdatedAt = time.Now()
	if o.FilledQuantity >= o.Quantity {
		o.Status = OrderStatusFilled
	} else {
		o.Status = OrderStatusPartial
	}
}

// OrderFilter holds filtering criteria for querying orders.
type OrderFilter struct {
	StockCode string
	Status    OrderStatus
	UserID    string
	Page      int
	PerPage   int
}
