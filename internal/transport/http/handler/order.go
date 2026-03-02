// Package handler contains the HTTP request handlers for the trading API.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	apporder "github.com/izzam/mini-exchange/internal/app/order"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
	"github.com/izzam/mini-exchange/internal/transport/http/middleware"
	"github.com/izzam/mini-exchange/pkg/response"
	"github.com/izzam/mini-exchange/pkg/validator"
)

// OrderHandler handles HTTP requests for the /orders resource.
type OrderHandler struct {
	svc *apporder.Service
}

// NewOrderHandler creates an OrderHandler.
func NewOrderHandler(svc *apporder.Service) *OrderHandler {
	return &OrderHandler{svc: svc}
}

// createOrderBody is the JSON request body for POST /orders.
type createOrderBody struct {
	StockCode string `json:"stock_code"`
	Side      string `json:"side"`
	Price     int64  `json:"price"`
	Quantity  int64  `json:"quantity"`
}

// Create handles POST /api/v1/orders.
func (h *OrderHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body createOrderBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	req, fieldErrs := validator.ValidateCreateOrder(body.StockCode, body.Side, body.Price, body.Quantity)
	if len(fieldErrs) > 0 {
		response.ValidationError(w, fieldErrs)
		return
	}

	// Extract user ID from context (set by auth middleware; fallback to "anonymous").
	userID := userIDFromContext(r)

	order, err := h.svc.CreateOrder(r.Context(), userID, req)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	response.JSON(w, http.StatusCreated, order)
}

// List handles GET /api/v1/orders.
// Query params: stock_code, status, user_id, page, per_page
func (h *OrderHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page := validator.ParsePage(intParam(q.Get("page"), 1))
	perPage := validator.ParsePerPage(intParam(q.Get("per_page"), validator.DefaultPerPage))

	// user_id filter: explicit query param takes precedence;
	// fall back to the authenticated user if no param given.
	userID := q.Get("user_id")
	if userID == "" {
		// Only scope to the authenticated user when auth is active.
		// With no auth middleware the context user is "anonymous", which
		// would hide all real orders — so we leave the filter empty.
		ctxUser := userIDFromContext(r)
		if ctxUser != "anonymous" {
			userID = ctxUser
		}
	}

	filter := entity.OrderFilter{
		StockCode: q.Get("stock_code"),
		Status:    entity.OrderStatus(q.Get("status")),
		UserID:    userID,
		Page:      page,
		PerPage:   perPage,
	}

	orders, total, err := h.svc.GetOrders(r.Context(), filter)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	response.Paginated(w, http.StatusOK, orders, response.NewMeta(total, page, perPage))
}

// GetByID handles GET /api/v1/orders/{id}.
func (h *OrderHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		response.Error(w, http.StatusBadRequest, "BAD_REQUEST", "missing order id")
		return
	}

	order, err := h.svc.GetOrderByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
			return
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	response.JSON(w, http.StatusOK, order)
}

// intParam parses a string query param as int, returning def on failure.
func intParam(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

// userIDFromContext extracts the authenticated user ID from the request context.
// Returns "anonymous" if no auth middleware has populated it.
func userIDFromContext(r *http.Request) string {
	if id, ok := middleware.UserIDFromContext(r.Context()); ok {
		return id
	}
	return "anonymous"
}
