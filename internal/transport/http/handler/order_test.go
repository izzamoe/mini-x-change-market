package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	apporder "github.com/izzam/mini-exchange/internal/app/order"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/engine"
	"github.com/izzam/mini-exchange/internal/infra/broker"
	"github.com/izzam/mini-exchange/internal/infra/storage/memory"
	"github.com/izzam/mini-exchange/internal/transport/http/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

type apiEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *apiErr         `json:"error"`
	Meta    *struct {
		Total   int64 `json:"total"`
		Page    int   `json:"page"`
		PerPage int   `json:"per_page"`
	} `json:"meta"`
}

type apiErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"details"`
}

func newOrderHarness(t *testing.T) (*handler.OrderHandler, *engine.Engine, *memory.OrderRepo, func()) {
	t.Helper()

	stocks := []entity.Stock{
		{Code: "BBCA", Name: "Bank BCA", BasePrice: 9500, TickSize: 25},
	}
	orderRepo := memory.NewOrderRepo()
	tradeRepo := memory.NewTradeRepo()
	marketRepo := memory.NewMarketRepo()
	bus := broker.NewEventBus()
	bus.Start()

	ctx := context.Background()
	for _, s := range stocks {
		require.NoError(t, marketRepo.InitStock(ctx, s))
	}

	eng := engine.NewEngine(stocks, bus, orderRepo, tradeRepo, marketRepo)
	eng.Start(ctx)

	svc := apporder.NewService(orderRepo, eng, bus)
	h := handler.NewOrderHandler(svc)

	cleanup := func() {
		eng.Stop()
		bus.Stop()
	}
	return h, eng, orderRepo, cleanup
}

func postOrder(h *handler.OrderHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Create(w, req)
	return w
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestOrderHandler_Create_Success(t *testing.T) {
	h, _, _, cleanup := newOrderHarness(t)
	defer cleanup()

	body := `{"stock_code":"BBCA","side":"BUY","price":9500,"quantity":100}`
	w := postOrder(h, body)

	assert.Equal(t, http.StatusCreated, w.Code)

	var env apiEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.True(t, env.Success)
	assert.NotNil(t, env.Data)
}

func TestOrderHandler_Create_ValidationError_MissingFields(t *testing.T) {
	h, _, _, cleanup := newOrderHarness(t)
	defer cleanup()

	body := `{"stock_code":"","side":"","price":0,"quantity":0}`
	w := postOrder(h, body)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var env apiEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.False(t, env.Success)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION_ERROR", env.Error.Code)
	assert.NotEmpty(t, env.Error.Details)
}

func TestOrderHandler_Create_ValidationError_InvalidSide(t *testing.T) {
	h, _, _, cleanup := newOrderHarness(t)
	defer cleanup()

	body := `{"stock_code":"BBCA","side":"HOLD","price":9500,"quantity":100}`
	w := postOrder(h, body)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestOrderHandler_Create_ValidationError_UnknownStock(t *testing.T) {
	h, _, _, cleanup := newOrderHarness(t)
	defer cleanup()

	body := `{"stock_code":"UNKNOWN","side":"BUY","price":9500,"quantity":100}`
	w := postOrder(h, body)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestOrderHandler_Create_BadJSON(t *testing.T) {
	h, _, _, cleanup := newOrderHarness(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Create(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestOrderHandler_List(t *testing.T) {
	h, _, orderRepo, cleanup := newOrderHarness(t)
	defer cleanup()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		o := &entity.Order{
			ID: fmt.Sprintf("o%d", i), UserID: "u1", StockCode: "BBCA",
			Side: entity.SideBuy, Price: 9500, Quantity: 100,
			Status: entity.OrderStatusOpen,
		}
		require.NoError(t, orderRepo.Save(ctx, o))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders?stock_code=BBCA&page=1&per_page=10", nil)
	w := httptest.NewRecorder()
	h.List(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var env apiEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.True(t, env.Success)
	require.NotNil(t, env.Meta)
	assert.EqualValues(t, 3, env.Meta.Total)
}

func TestOrderHandler_GetByID_Found(t *testing.T) {
	h, _, orderRepo, cleanup := newOrderHarness(t)
	defer cleanup()

	ctx := context.Background()
	o := &entity.Order{
		ID: "test-id", UserID: "u1", StockCode: "BBCA",
		Side: entity.SideBuy, Price: 9500, Quantity: 100,
		Status: entity.OrderStatusOpen,
	}
	require.NoError(t, orderRepo.Save(ctx, o))

	// Use Chi router to get URL params populated.
	router := chi.NewRouter()
	router.Get("/orders/{id}", h.GetByID)

	req := httptest.NewRequest(http.MethodGet, "/orders/test-id", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOrderHandler_GetByID_NotFound(t *testing.T) {
	h, _, _, cleanup := newOrderHarness(t)
	defer cleanup()

	router := chi.NewRouter()
	router.Get("/orders/{id}", h.GetByID)

	req := httptest.NewRequest(http.MethodGet, "/orders/does-not-exist", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
