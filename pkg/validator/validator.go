// Package validator provides input validation helpers for the trading system.
package validator

import (
	"fmt"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/pkg/response"
)

const (
	MinPrice    int64 = 1
	MaxPrice    int64 = 999_999_999
	MinQuantity int64 = 1
	MaxQuantity int64 = 1_000_000

	DefaultPage    = 1
	DefaultPerPage = 20
	MaxPerPage     = 100
)

// CreateOrderRequest holds the validated fields for creating an order.
type CreateOrderRequest struct {
	StockCode string
	Side      entity.Side
	Price     int64
	Quantity  int64
}

// ValidateCreateOrder validates an incoming create-order payload and returns
// a slice of field errors (nil means valid).
func ValidateCreateOrder(stockCode, side string, price, quantity int64) (CreateOrderRequest, []response.FieldError) {
	var errs []response.FieldError

	// StockCode
	if stockCode == "" {
		errs = append(errs, response.FieldError{Field: "stock_code", Message: "stock_code is required"})
	} else if !entity.IsValidStockCode(stockCode) {
		errs = append(errs, response.FieldError{Field: "stock_code", Message: fmt.Sprintf("unknown stock code: %s", stockCode)})
	}

	// Side
	s := entity.Side(side)
	if side == "" {
		errs = append(errs, response.FieldError{Field: "side", Message: "side is required"})
	} else if !s.IsValid() {
		errs = append(errs, response.FieldError{Field: "side", Message: "side must be BUY or SELL"})
	}

	// Price
	if price < MinPrice {
		errs = append(errs, response.FieldError{Field: "price", Message: fmt.Sprintf("price must be at least %d", MinPrice)})
	} else if price > MaxPrice {
		errs = append(errs, response.FieldError{Field: "price", Message: fmt.Sprintf("price must not exceed %d", MaxPrice)})
	}

	// Quantity
	if quantity < MinQuantity {
		errs = append(errs, response.FieldError{Field: "quantity", Message: fmt.Sprintf("quantity must be at least %d", MinQuantity)})
	} else if quantity > MaxQuantity {
		errs = append(errs, response.FieldError{Field: "quantity", Message: fmt.Sprintf("quantity must not exceed %d", MaxQuantity)})
	}

	if len(errs) > 0 {
		return CreateOrderRequest{}, errs
	}
	return CreateOrderRequest{
		StockCode: stockCode,
		Side:      s,
		Price:     price,
		Quantity:  quantity,
	}, nil
}

// ParsePage returns a sanitised page number (>= 1).
func ParsePage(v int) int {
	if v < 1 {
		return DefaultPage
	}
	return v
}

// ParsePerPage returns a sanitised per-page value clamped to [1, MaxPerPage].
func ParsePerPage(v int) int {
	if v < 1 {
		return DefaultPerPage
	}
	if v > MaxPerPage {
		return MaxPerPage
	}
	return v
}
