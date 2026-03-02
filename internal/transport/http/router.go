// Package http wires together the Chi router, middleware stack, and handlers.
package http

import (
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	appmarket "github.com/izzam/mini-exchange/internal/app/market"
	apporder "github.com/izzam/mini-exchange/internal/app/order"
	apptrade "github.com/izzam/mini-exchange/internal/app/trade"
	"github.com/izzam/mini-exchange/internal/infra/auth"
	"github.com/izzam/mini-exchange/internal/transport/http/handler"
	"github.com/izzam/mini-exchange/internal/transport/http/middleware"
	"github.com/izzam/mini-exchange/pkg/response"
)

// RouterDeps bundles all dependencies required to build the router.
type RouterDeps struct {
	OrderSvc    *apporder.Service
	TradeSvc    *apptrade.Service
	MarketSvc   *appmarket.Service
	AuthSvc     *auth.Service // optional — if nil, auth routes and JWT middleware are disabled
	RateLimiter *middleware.RateLimiter
	// WSHandler is the WebSocket upgrade handler, mounted at /ws.
	// Optional — if nil the /ws route is not registered.
	WSHandler http.Handler
}

var startTime = time.Now()

// NewRouter builds and returns a fully configured Chi router.
func NewRouter(deps RouterDeps) http.Handler {
	r := chi.NewRouter()

	// Global middleware (outermost = first to run).
	r.Use(chimw.RequestID)
	r.Use(middleware.Recovery)
	r.Use(middleware.Logger)
	r.Use(middleware.CORS)
	r.Use(middleware.PrometheusMiddleware)
	if deps.RateLimiter != nil {
		r.Use(deps.RateLimiter.Middleware)
	}

	// Observability + health.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		response.JSON(w, http.StatusOK, map[string]interface{}{
			"status":     "ok",
			"uptime":     fmt.Sprintf("%.0fs", time.Since(startTime).Seconds()),
			"goroutines": runtime.NumGoroutine(),
		})
	})
	r.Handle("/metrics", promhttp.Handler())

	// WebSocket endpoint — auth middleware extracts user from ?token= if present.
	if deps.WSHandler != nil {
		if deps.AuthSvc != nil {
			r.With(middleware.Auth(deps.AuthSvc, false)).Handle("/ws", deps.WSHandler)
		} else {
			r.Handle("/ws", deps.WSHandler)
		}
	}

	// API routes.
	orderH := handler.NewOrderHandler(deps.OrderSvc)
	tradeH := handler.NewTradeHandler(deps.TradeSvc)
	marketH := handler.NewMarketHandler(deps.MarketSvc)

	r.Route("/api/v1", func(r chi.Router) {
		// Auth (optional — only registered when AuthSvc is provided).
		if deps.AuthSvc != nil {
			authH := handler.NewAuthHandler(deps.AuthSvc)
			r.Post("/auth/register", authH.Register)
			r.Post("/auth/login", authH.Login)
		}

		// Orders — optional JWT extraction (required=false: anonymous fallback).
		ordersRouter := r.With()
		if deps.AuthSvc != nil {
			ordersRouter = r.With(middleware.Auth(deps.AuthSvc, false))
		}
		ordersRouter.Post("/orders", orderH.Create)
		ordersRouter.Get("/orders", orderH.List)
		ordersRouter.Get("/orders/{id}", orderH.GetByID)

		// Trades
		r.Get("/trades", tradeH.List)

		// Market data
		r.Get("/market/ticker", marketH.AllTickers)
		r.Get("/market/ticker/{stock}", marketH.Ticker)
		r.Get("/market/orderbook/{stock}", marketH.OrderBook)
		r.Get("/market/trades/{stock}", marketH.RecentTrades)
	})

	return r
}
