// Package main — unit tests for horizontal-scaling helper functions.
//
// These tests exercise the pure utility functions in main.go that support
// consistent-hash partitioning without requiring any network connections.
package main

import (
	"testing"

	"github.com/izzam/mini-exchange/internal/domain/entity"
)

// ── stockCodes ────────────────────────────────────────────────────────────────

func TestStockCodes_ReturnsAllCodes(t *testing.T) {
	stocks := []entity.Stock{
		{Code: "BBCA"},
		{Code: "BBRI"},
		{Code: "TLKM"},
	}
	codes := stockCodes(stocks)
	if len(codes) != 3 {
		t.Fatalf("expected 3 codes, got %d", len(codes))
	}
	for i, want := range []string{"BBCA", "BBRI", "TLKM"} {
		if codes[i] != want {
			t.Errorf("codes[%d] = %q, want %q", i, codes[i], want)
		}
	}
}

func TestStockCodes_EmptySlice(t *testing.T) {
	codes := stockCodes(nil)
	if len(codes) != 0 {
		t.Fatalf("expected 0 codes, got %d", len(codes))
	}
}

func TestStockCodes_PreservesOrder(t *testing.T) {
	stocks := entity.DefaultStocks()
	codes := stockCodes(stocks)
	if len(codes) != len(stocks) {
		t.Fatalf("length mismatch: got %d, want %d", len(codes), len(stocks))
	}
	for i, s := range stocks {
		if codes[i] != s.Code {
			t.Errorf("codes[%d] = %q, want %q", i, codes[i], s.Code)
		}
	}
}

// ── filterStocks ──────────────────────────────────────────────────────────────

func TestFilterStocks_ReturnsAllowedOnly(t *testing.T) {
	stocks := []entity.Stock{
		{Code: "BBCA"},
		{Code: "BBRI"},
		{Code: "TLKM"},
		{Code: "ASII"},
	}
	allowed := []string{"BBCA", "TLKM"}
	result := filterStocks(stocks, allowed)
	if len(result) != 2 {
		t.Fatalf("expected 2 stocks, got %d", len(result))
	}
	codes := map[string]bool{}
	for _, s := range result {
		codes[s.Code] = true
	}
	for _, c := range allowed {
		if !codes[c] {
			t.Errorf("expected %q in result", c)
		}
	}
}

func TestFilterStocks_EmptyAllowed(t *testing.T) {
	stocks := entity.DefaultStocks()
	result := filterStocks(stocks, nil)
	if len(result) != 0 {
		t.Fatalf("expected 0 stocks, got %d", len(result))
	}
}

func TestFilterStocks_AllAllowed(t *testing.T) {
	stocks := entity.DefaultStocks()
	codes := stockCodes(stocks)
	result := filterStocks(stocks, codes)
	if len(result) != len(stocks) {
		t.Fatalf("expected %d stocks, got %d", len(stocks), len(result))
	}
}

func TestFilterStocks_NonExistentCodeIgnored(t *testing.T) {
	stocks := []entity.Stock{
		{Code: "BBCA"},
		{Code: "BBRI"},
	}
	// "XXXX" does not exist — should be silently ignored.
	result := filterStocks(stocks, []string{"BBCA", "XXXX"})
	if len(result) != 1 {
		t.Fatalf("expected 1 stock, got %d", len(result))
	}
	if result[0].Code != "BBCA" {
		t.Errorf("expected BBCA, got %q", result[0].Code)
	}
}

func TestFilterStocks_PreservesFieldValues(t *testing.T) {
	stocks := []entity.Stock{
		{Code: "BBCA", Name: "Bank BCA", BasePrice: 9500, TickSize: 25},
	}
	result := filterStocks(stocks, []string{"BBCA"})
	if len(result) != 1 {
		t.Fatalf("expected 1 stock")
	}
	s := result[0]
	if s.Name != "Bank BCA" || s.BasePrice != 9500 || s.TickSize != 25 {
		t.Errorf("field values not preserved: %+v", s)
	}
}

// ── subjectSuffix ─────────────────────────────────────────────────────────────

func TestSubjectSuffix_TradeSubject(t *testing.T) {
	got := subjectSuffix("trade.BBCA")
	if got != "BBCA" {
		t.Errorf("got %q, want %q", got, "BBCA")
	}
}

func TestSubjectSuffix_OrderSubject(t *testing.T) {
	got := subjectSuffix("order.user-123")
	if got != "user-123" {
		t.Errorf("got %q, want %q", got, "user-123")
	}
}

func TestSubjectSuffix_TickerSubject(t *testing.T) {
	got := subjectSuffix("ticker.TLKM")
	if got != "TLKM" {
		t.Errorf("got %q, want %q", got, "TLKM")
	}
}

func TestSubjectSuffix_NoDot_ReturnsInput(t *testing.T) {
	got := subjectSuffix("nodot")
	if got != "nodot" {
		t.Errorf("got %q, want %q", got, "nodot")
	}
}

func TestSubjectSuffix_TrailingDot_ReturnsInput(t *testing.T) {
	// A subject ending in "." has nothing after the dot — returns full string.
	got := subjectSuffix("trade.")
	if got != "trade." {
		t.Errorf("got %q, want %q", got, "trade.")
	}
}

func TestSubjectSuffix_EngineSubjectWithMultipleDots(t *testing.T) {
	// "engine.orders.BBCA" — only the first dot is stripped.
	got := subjectSuffix("engine.orders.BBCA")
	if got != "orders.BBCA" {
		t.Errorf("got %q, want %q", got, "orders.BBCA")
	}
}
