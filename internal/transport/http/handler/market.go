package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	appmarket "github.com/izzam/mini-exchange/internal/app/market"
	"github.com/izzam/mini-exchange/internal/domain/repository"
	"github.com/izzam/mini-exchange/pkg/response"
)

// MarketHandler handles HTTP requests for the /market resource.
type MarketHandler struct {
	svc *appmarket.Service
}

// NewMarketHandler creates a MarketHandler.
func NewMarketHandler(svc *appmarket.Service) *MarketHandler {
	return &MarketHandler{svc: svc}
}

// AllTickers handles GET /api/v1/market/ticker.
func (h *MarketHandler) AllTickers(w http.ResponseWriter, r *http.Request) {
	tickers, err := h.svc.GetAllTickers(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	response.JSON(w, http.StatusOK, tickers)
}

// Ticker handles GET /api/v1/market/ticker/{stock}.
func (h *MarketHandler) Ticker(w http.ResponseWriter, r *http.Request) {
	stock := chi.URLParam(r, "stock")
	ticker, err := h.svc.GetTicker(r.Context(), stock)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "stock not found")
			return
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	response.JSON(w, http.StatusOK, ticker)
}

// OrderBook handles GET /api/v1/market/orderbook/{stock}.
func (h *MarketHandler) OrderBook(w http.ResponseWriter, r *http.Request) {
	stock := chi.URLParam(r, "stock")
	book, err := h.svc.GetOrderBook(r.Context(), stock)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "stock not found")
			return
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	response.JSON(w, http.StatusOK, book)
}

// RecentTrades handles GET /api/v1/market/trades/{stock}.
// Query param: limit (default 20, max 100)
func (h *MarketHandler) RecentTrades(w http.ResponseWriter, r *http.Request) {
	stock := chi.URLParam(r, "stock")
	limit := intParam(r.URL.Query().Get("limit"), 20)
	if limit > 100 {
		limit = 100
	}

	trades, err := h.svc.GetRecentTrades(r.Context(), stock, limit)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	response.JSON(w, http.StatusOK, trades)
}
