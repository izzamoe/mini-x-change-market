package entity

import "time"

// Ticker holds the latest market data snapshot for a single stock.
type Ticker struct {
	StockCode string    `json:"stock_code"`
	LastPrice int64     `json:"last_price"`
	PrevClose int64     `json:"prev_close"`
	Change    int64     `json:"change"`     // LastPrice - PrevClose
	ChangePct float64   `json:"change_pct"` // percentage change
	High      int64     `json:"high"`       // session high
	Low       int64     `json:"low"`        // session low
	Volume    int64     `json:"volume"`     // total traded volume this session
	UpdatedAt time.Time `json:"updated_at"`
}

// UpdateFromTrade recalculates ticker fields after a trade is executed.
func (t *Ticker) UpdateFromTrade(price, qty int64) {
	t.LastPrice = price
	t.Volume += qty
	if price > t.High {
		t.High = price
	}
	if price < t.Low || t.Low == 0 {
		t.Low = price
	}
	if t.PrevClose > 0 {
		t.Change = t.LastPrice - t.PrevClose
		t.ChangePct = float64(t.Change) / float64(t.PrevClose) * 100
	}
	t.UpdatedAt = time.Now()
}
