package handler

import (
	"net/http"

	apptrade "github.com/izzam/mini-exchange/internal/app/trade"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/pkg/response"
	"github.com/izzam/mini-exchange/pkg/validator"
)

// TradeHandler handles HTTP requests for the /trades resource.
type TradeHandler struct {
	svc *apptrade.Service
}

// NewTradeHandler creates a TradeHandler.
func NewTradeHandler(svc *apptrade.Service) *TradeHandler {
	return &TradeHandler{svc: svc}
}

// List handles GET /api/v1/trades.
// Query params: stock_code, page, per_page
func (h *TradeHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page := validator.ParsePage(intParam(q.Get("page"), 1))
	perPage := validator.ParsePerPage(intParam(q.Get("per_page"), validator.DefaultPerPage))

	filter := entity.TradeFilter{
		StockCode: q.Get("stock_code"),
		UserID:    q.Get("user_id"),
		Page:      page,
		PerPage:   perPage,
	}

	trades, total, err := h.svc.GetTradeHistory(r.Context(), filter)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	response.Paginated(w, http.StatusOK, trades, response.NewMeta(total, page, perPage))
}
