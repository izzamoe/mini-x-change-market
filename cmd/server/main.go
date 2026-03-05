// Package main is the entry point for the mini-exchange trading server.
// It wires all dependencies together, starts the HTTP server, and handles
// graceful shutdown when SIGINT or SIGTERM is received.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/izzam/mini-exchange/config"
	"github.com/izzam/mini-exchange/internal/app/market"
	"github.com/izzam/mini-exchange/internal/app/order"
	"github.com/izzam/mini-exchange/internal/app/trade"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/event"
	"github.com/izzam/mini-exchange/internal/engine"
	"github.com/izzam/mini-exchange/internal/infra/auth"
	"github.com/izzam/mini-exchange/internal/infra/broker"
	"github.com/izzam/mini-exchange/internal/infra/broker/natsbroker"
	"github.com/izzam/mini-exchange/internal/infra/storage/memory"
	postgrestore "github.com/izzam/mini-exchange/internal/infra/storage/postgres"
	redistore "github.com/izzam/mini-exchange/internal/infra/storage/redis"
	ipartition "github.com/izzam/mini-exchange/internal/partition"
	"github.com/izzam/mini-exchange/internal/simulator"
	httptransport "github.com/izzam/mini-exchange/internal/transport/http"
	"github.com/izzam/mini-exchange/internal/transport/http/middleware"
	"github.com/izzam/mini-exchange/internal/transport/ws"
	"github.com/izzam/mini-exchange/internal/worker"
	pkgpartition "github.com/izzam/mini-exchange/pkg/partition"
)

func main() {
	// 1. Load configuration from environment variables.
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	// 2. Configure structured logger.
	setupLogger(cfg.Log.Level, cfg.Log.Format)

	slog.Info("mini-exchange starting",
		"port", cfg.Server.Port,
		"storage", cfg.Storage.Type,
		"auth", cfg.Auth.Enabled,
		"nats", cfg.NATS.Enabled,
		"redis", cfg.Redis.Enabled,
		"binance_feed", cfg.Binance.Enabled,
		"simulator", cfg.Simulator.Enabled,
		"partition_enabled", cfg.Partition.Enabled,
	)

	// 3. Root context – cancelled on SIGINT / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 4. Initialise in-memory storage (always).
	memOrderRepo := memory.NewOrderRepo()
	memTradeRepo := memory.NewTradeRepo()
	marketRepo := memory.NewMarketRepo()

	// 5. Optional PostgreSQL pool + DB Worker.
	var pgPool *pgxpool.Pool
	var dbWorker *worker.DBWorker
	if cfg.DBWorker.Enabled || cfg.Storage.Type == "postgres" {
		pool, err := pgxpool.New(ctx, cfg.Storage.DatabaseURL)
		if err != nil {
			slog.Warn("postgres connection failed — running without DB worker", "error", err)
		} else {
			pgPool = pool
			slog.Info("postgres connected")
		}
	}
	if cfg.DBWorker.Enabled {
		dbWorkerCfg := worker.DBWorkerConfig{
			BatchSize:     cfg.DBWorker.BatchSize,
			FlushInterval: cfg.DBWorker.FlushInterval,
			ChannelSize:   cfg.DBWorker.ChannelSize,
			RetryMax:      cfg.DBWorker.RetryMax,
			RetryBackoff:  cfg.DBWorker.RetryBackoff,
		}
		dbWorker = worker.NewDBWorker(pgPool, dbWorkerCfg)
		go dbWorker.Start(ctx)
		// Periodically export queue size to Prometheus.
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					middleware.Metrics.DBWorkerQueueSize.Set(float64(dbWorker.QueueLen()))
				case <-ctx.Done():
					return
				}
			}
		}()
		slog.Info("db worker started",
			"batch_size", cfg.DBWorker.BatchSize,
			"flush_interval", cfg.DBWorker.FlushInterval,
		)
	}

	// Resolve concrete order/trade repos: PostgreSQL when STORAGE_TYPE=postgres
	// and the pool is available; otherwise fall back to in-memory.
	var orderRepo interface {
		Save(ctx context.Context, order *entity.Order) error
		FindByID(ctx context.Context, id string) (*entity.Order, error)
		FindAll(ctx context.Context, filter entity.OrderFilter) ([]*entity.Order, int64, error)
		Update(ctx context.Context, order *entity.Order) error
	} = memOrderRepo
	var tradeRepo interface {
		Save(ctx context.Context, trade *entity.Trade) error
		FindByStockCode(ctx context.Context, stockCode string, limit int) ([]*entity.Trade, error)
		FindAll(ctx context.Context, filter entity.TradeFilter) ([]*entity.Trade, int64, error)
	} = memTradeRepo

	if cfg.Storage.Type == "postgres" && pgPool != nil {
		orderRepo = postgrestore.NewOrderRepo(pgPool)
		tradeRepo = postgrestore.NewTradeRepo(pgPool)
		slog.Info("using postgresql storage")
	} else {
		slog.Info("using in-memory storage")
	}

	// 6. Initialise and start the event bus.
	eventBus := broker.NewEventBus()
	eventBus.Start()

	// 7. Optional NATS publisher + subscriber for cross-instance event fan-out.
	var natsPub *natsbroker.Publisher
	var natsSub *natsbroker.Subscriber
	if cfg.NATS.Enabled {
		pub, err := natsbroker.NewPublisher(cfg.NATS.URL)
		if err != nil {
			slog.Warn("nats publisher unavailable — broadcasting limited to this instance", "error", err)
		} else {
			natsPub = pub
			slog.Info("nats publisher connected", "url", cfg.NATS.URL)
		}

		sub, err := natsbroker.NewSubscriber(cfg.NATS.URL)
		if err != nil {
			slog.Warn("nats subscriber unavailable — cross-instance WS broadcast disabled", "error", err)
		} else {
			natsSub = sub
			slog.Info("nats subscriber connected", "url", cfg.NATS.URL)
		}
	}

	// 8. Optional Redis cache.
	// When NATS is disabled, Redis PubSub is wired as fallback cross-instance
	// broadcast so that WS clients across instances still get all events.
	var redisCache *redistore.Cache
	var redisPubSub *redistore.PubSub
	if cfg.Redis.Enabled {
		cache, err := redistore.NewCache(cfg.Redis.URL)
		if err != nil {
			slog.Warn("redis cache unavailable", "error", err)
		} else if err := cache.Ping(ctx); err != nil {
			slog.Warn("redis ping failed — cache disabled", "error", err)
		} else {
			redisCache = cache
			slog.Info("redis cache connected", "url", cfg.Redis.URL)
		}

		// Redis PubSub: activate only when NATS is off (NATS is the preferred
		// mechanism; using both would cause duplicate WS broadcasts).
		if !cfg.NATS.Enabled {
			ps, err := redistore.NewPubSub(cfg.Redis.URL)
			if err != nil {
				slog.Warn("redis pubsub unavailable", "error", err)
			} else {
				redisPubSub = ps
				slog.Info("redis pubsub enabled (nats-disabled fallback for cross-instance WS broadcast)")
			}
		}
	}

	// 9. Determine which stocks to load.
	stocks := entity.DefaultStocks()
	if cfg.Binance.Enabled {
		stocks = append(stocks, entity.BinanceStocks()...)
	}

	// 10. Optional consistent-hash partitioner.
	// When enabled, each instance owns a deterministic subset of stocks and
	// only starts matching-engine goroutines for those stocks.
	var partitioner *pkgpartition.Partitioner
	if cfg.Partition.Enabled {
		p, err := pkgpartition.New(cfg.Partition.InstanceIndex, cfg.Partition.TotalInstances)
		if err != nil {
			slog.Error("partition config invalid", "error", err)
			os.Exit(1)
		}
		partitioner = p
		owned := p.OwnedStocks(stockCodes(stocks))
		slog.Info("partition enabled",
			"instance_index", cfg.Partition.InstanceIndex,
			"total_instances", cfg.Partition.TotalInstances,
			"owned_stocks", owned,
			"owned_count", len(owned),
		)
	}

	// 11. Initialise and start the matching engine.
	// When partitioning is enabled, only start matchers for owned stocks so
	// that non-owned stocks never consume goroutines on this instance.
	engineStocks := stocks
	if partitioner != nil {
		ownedCodes := partitioner.OwnedStocks(stockCodes(stocks))
		engineStocks = filterStocks(stocks, ownedCodes)
		slog.Info("matching engine restricted to owned stocks",
			"owned", len(engineStocks), "total", len(stocks))
	}
	eng := engine.NewEngine(engineStocks, eventBus, orderRepo, tradeRepo, marketRepo)
	eng.Start(ctx)

	// 12. Build the order submitter: plain engine or partitioned router.
	// The partitioned router forwards orders for non-owned stocks via NATS
	// to the instance that owns them; the owning instance's QueueSubscribe
	// handler (wired in step 20) submits them locally.
	var submitter order.OrderSubmitter = eng
	if partitioner != nil && natsPub != nil {
		submitter = ipartition.NewRouter(eng, natsPub, partitioner)
		slog.Info("partitioned engine router active",
			"instance", cfg.Partition.InstanceIndex,
			"total", cfg.Partition.TotalInstances,
		)
	}

	// 13. Initialise application services.
	orderSvc := order.NewService(orderRepo, submitter, eventBus)
	tradeSvc := trade.NewService(tradeRepo)
	marketSvc := market.NewService(marketRepo, tradeRepo, eng)

	// Attach Redis cache-aside to market service when available.
	if redisCache != nil {
		marketSvc.SetCache(redisCache)
		slog.Info("redis cache-aside enabled for market service")
	}

	// 14. Optional JWT auth service.
	var authSvc *auth.Service
	if cfg.Auth.Enabled {
		authSvc = auth.NewService(cfg.Auth.JWTSecret, cfg.Auth.JWTExpiry)

		// Wire PostgreSQL user repo when pool is available.
		if pgPool != nil {
			userRepo := postgrestore.NewUserRepo(pgPool)
			authSvc.SetUserRepo(userRepo)
			if err := authSvc.LoadUsers(ctx); err != nil {
				slog.Warn("failed to load users from database", "error", err)
			}
		}

		slog.Info("jwt auth enabled")
	}

	// 15. Initialise and start the price simulator.
	// When Binance feed is enabled, simulator is disabled to avoid duplicate/conflicting price sources.
	var sim *simulator.Simulator
	if cfg.Simulator.Enabled && !cfg.Binance.Enabled {
		simCfg := simulator.SimulatorConfig{
			IntervalMin: cfg.Simulator.IntervalMin,
			IntervalMax: cfg.Simulator.IntervalMax,
		}
		sim = simulator.NewSimulator(entity.DefaultStocks(), eventBus, marketRepo, simCfg)
		go func() {
			if err := sim.Start(ctx); err != nil && ctx.Err() == nil {
				slog.Error("simulator error", "error", err)
			}
		}()
		slog.Info("internal price simulator started")
	} else if cfg.Binance.Enabled {
		slog.Info("simulator skipped - using binance feed as price source")
	}

	// 16. Optionally start the Binance external feed.
	if cfg.Binance.Enabled {
		binanceCfg := simulator.BinanceConfig{
			WSURL:      cfg.Binance.WSURL,
			Symbols:    simulator.DefaultBinanceConfig().Symbols,
			MaxBackoff: cfg.Binance.ReconnectMax,
		}
		binanceFeed := simulator.NewBinanceFeed(binanceCfg, eventBus, marketRepo, entity.BinanceStocks())
		go func() {
			if err := binanceFeed.Start(ctx); err != nil && ctx.Err() == nil {
				slog.Error("binance feed error", "error", err)
			}
		}()
	}

	// 17. Initialise the WebSocket hub and start its event loop.
	hub := ws.NewHub()
	go hub.Run(ctx)

	// 18. Wire domain events → WebSocket hub + optional NATS / Redis PubSub + Redis cache + DB worker.
	wireEvents(ctx, eventBus, hub, natsPub, redisPubSub, redisCache, dbWorker)

	// 19. Wire NATS subscriber → local WebSocket hub (cross-instance WS broadcast).
	if natsSub != nil {
		wireNATSSubscriber(natsSub, hub)
	}

	// 20. Wire NATS queue-group subscriber for matching-engine order routing.
	// Only the instance that owns a stock subscribes to its engine subject.
	// NATS queue groups guarantee exactly-once delivery per stock.
	if partitioner != nil && natsSub != nil {
		wireEngineNATSOrders(natsSub, eng, partitioner, stocks)
	}

	// 21. Wire Redis PubSub subscriber → local WS hub (active only when NATS is off).
	if redisPubSub != nil {
		wireRedisPubSubSubscriber(ctx, redisPubSub, hub, stocks)
	}

	// 22. Initialise optional rate limiter.
	var rateLimiter *middleware.RateLimiter
	if cfg.RateLimit.Enabled {
		rateLimiter = middleware.NewRateLimiter(cfg.RateLimit.RPS, cfg.RateLimit.Burst)
	}

	// 23. Build the HTTP router.
	wsHandler := ws.NewHandler(hub)
	router := httptransport.NewRouter(httptransport.RouterDeps{
		OrderSvc:    orderSvc,
		TradeSvc:    tradeSvc,
		MarketSvc:   marketSvc,
		AuthSvc:     authSvc,
		RateLimiter: rateLimiter,
		WSHandler:   wsHandler,
	})

	// 24. Create and start the HTTP server.
	srv := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// 25. Block until shutdown signal or server error.
	select {
	case err := <-serverErr:
		slog.Error("server error", "error", err)
		stop()
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	}

	// 26. Graceful shutdown — ordered to drain cleanly.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	// a) Stop accepting new HTTP requests.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "error", err)
	}
	// b) Stop the matching engine (drains queued orders).
	eng.Stop()
	// c) Stop the simulator.
	if sim != nil {
		sim.Stop()
	}
	// d) Drain and stop the event bus.
	eventBus.Stop()
	// e) Stop the DB worker and wait for all pending writes to complete.
	// This must happen BEFORE closing the PostgreSQL connection pool.
	if dbWorker != nil {
		dbWorker.Stop()
	}
	// f) Flush NATS connections.
	if natsPub != nil {
		natsPub.Close()
	}
	if natsSub != nil {
		natsSub.Close()
	}
	// g) Close Redis.
	if redisCache != nil {
		_ = redisCache.Close()
	}
	if redisPubSub != nil {
		_ = redisPubSub.Close()
	}
	// h) Close Postgres pool.
	if pgPool != nil {
		pgPool.Close()
	}

	slog.Info("server stopped gracefully")
}

// wireEvents subscribes to domain events and forwards them to:
//   - the local WebSocket hub (always)
//   - NATS subjects (when natsPub != nil)
//   - Redis PubSub channels (when redisPubSub != nil; exclusive with NATS)
//   - Redis cache (when redisCache != nil) for ticker and orderbook snapshots
//   - the DB Worker (when dbw != nil)
func wireEvents(
	ctx context.Context,
	bus event.Bus,
	hub *ws.Hub,
	natsPub *natsbroker.Publisher,
	redisPubSub *redistore.PubSub,
	redisCache *redistore.Cache,
	dbw *worker.DBWorker,
) {
	bus.Subscribe(event.TradeExecuted, func(e event.Event) {
		hub.Broadcast("market.trade", e.StockCode, e.Payload)
		if natsPub != nil {
			_ = natsPub.Publish("trade."+e.StockCode, e.Payload)
		} else if redisPubSub != nil {
			if data, err := json.Marshal(e.Payload); err == nil {
				_ = redisPubSub.Publish(ctx, "trade."+e.StockCode, data)
			}
		}
		if dbw != nil {
			if t, ok := e.Payload.(*entity.Trade); ok {
				_ = dbw.Enqueue(worker.WriteOp{Type: worker.OpTradeSave, Payload: t})
			}
		}
		middleware.Metrics.TradesExecutedTotal.WithLabelValues(e.StockCode).Inc()
	})

	bus.Subscribe(event.TickerUpdated, func(e event.Event) {
		hub.Broadcast("market.ticker", e.StockCode, e.Payload)
		if natsPub != nil {
			_ = natsPub.Publish("ticker."+e.StockCode, e.Payload)
		} else if redisPubSub != nil {
			if data, err := json.Marshal(e.Payload); err == nil {
				_ = redisPubSub.Publish(ctx, "ticker."+e.StockCode, data)
			}
		}
		// Write-through: populate Redis cache on every ticker update.
		if redisCache != nil {
			if t, ok := e.Payload.(*entity.Ticker); ok {
				if err := redisCache.SetTicker(ctx, e.StockCode, t); err != nil {
					slog.Debug("redis set ticker error", "stock", e.StockCode, "error", err)
				}
			}
		}
	})

	bus.Subscribe(event.OrderBookUpdated, func(e event.Event) {
		hub.Broadcast("market.orderbook", e.StockCode, e.Payload)
		if natsPub != nil {
			_ = natsPub.Publish("orderbook."+e.StockCode, e.Payload)
		} else if redisPubSub != nil {
			if data, err := json.Marshal(e.Payload); err == nil {
				_ = redisPubSub.Publish(ctx, "orderbook."+e.StockCode, data)
			}
		}
		// Write-through: populate Redis cache on every order book update.
		if redisCache != nil {
			if b, ok := e.Payload.(*entity.OrderBook); ok {
				if err := redisCache.SetOrderBook(ctx, e.StockCode, b); err != nil {
					slog.Debug("redis set orderbook error", "stock", e.StockCode, "error", err)
				}
			}
		}
	})

	bus.Subscribe(event.OrderCreated, func(e event.Event) {
		if o, ok := e.Payload.(*entity.Order); ok {
			middleware.Metrics.OrdersCreatedTotal.WithLabelValues(o.StockCode, string(o.Side)).Inc()
			if dbw != nil {
				_ = dbw.Enqueue(worker.WriteOp{Type: worker.OpOrderSave, Payload: o})
			}
		}
	})

	bus.Subscribe(event.OrderUpdated, func(e event.Event) {
		o, ok := e.Payload.(*entity.Order)
		if !ok {
			return
		}
		hub.BroadcastToUser("order.update", o.UserID, e.Payload)
		if natsPub != nil {
			_ = natsPub.Publish("order."+o.UserID, e.Payload)
		} else if redisPubSub != nil {
			if data, err := json.Marshal(e.Payload); err == nil {
				_ = redisPubSub.Publish(ctx, "order.updates", data)
			}
		}
		if dbw != nil {
			_ = dbw.Enqueue(worker.WriteOp{Type: worker.OpOrderUpdate, Payload: o})
		}
	})
}

// wireNATSSubscriber subscribes to all cross-instance event subjects on NATS
// and forwards decoded payloads to the local WebSocket hub.
// This ensures clients connected to THIS instance receive events originally
// published by OTHER instances — enabling horizontal scaling.
//
// Subject convention (mirrors the publisher in wireEvents):
//
//	trade.<stock>      → hub.Broadcast("market.trade", stock, *entity.Trade)
//	ticker.<stock>     → hub.Broadcast("market.ticker", stock, *entity.Ticker)
//	orderbook.<stock>  → hub.Broadcast("market.orderbook", stock, *entity.OrderBook)
//	order.<userID>     → hub.BroadcastToUser("order.update", userID, *entity.Order)
func wireNATSSubscriber(sub *natsbroker.Subscriber, hub *ws.Hub) {
	type subjectHandler struct {
		subject string
		handler func(subj string, data []byte)
	}

	handlers := []subjectHandler{
		{
			subject: "trade.*",
			handler: func(subj string, data []byte) {
				stock := subjectSuffix(subj)
				var t entity.Trade
				if err := json.Unmarshal(data, &t); err == nil {
					hub.Broadcast("market.trade", stock, &t)
				}
			},
		},
		{
			subject: "ticker.*",
			handler: func(subj string, data []byte) {
				stock := subjectSuffix(subj)
				var t entity.Ticker
				if err := json.Unmarshal(data, &t); err == nil {
					hub.Broadcast("market.ticker", stock, &t)
				}
			},
		},
		{
			subject: "orderbook.*",
			handler: func(subj string, data []byte) {
				stock := subjectSuffix(subj)
				var b entity.OrderBook
				if err := json.Unmarshal(data, &b); err == nil {
					hub.Broadcast("market.orderbook", stock, &b)
				}
			},
		},
		{
			subject: "order.*",
			handler: func(subj string, data []byte) {
				userID := subjectSuffix(subj)
				var o entity.Order
				if err := json.Unmarshal(data, &o); err == nil {
					hub.BroadcastToUser("order.update", userID, &o)
				}
			},
		},
	}

	for _, h := range handlers {
		if err := sub.SubscribeSubject(h.subject, h.handler); err != nil {
			slog.Warn("nats subscribe failed", "subject", h.subject, "error", err)
		} else {
			slog.Info("nats subscriber wired", "subject", h.subject)
		}
	}
}

// wireEngineNATSOrders sets up NATS queue-group subscriptions for every stock
// owned by this instance. When another instance forwards an order via NATS
// (because it doesn't own the stock), this subscription delivers it to the
// local matching engine.
//
// Queue groups guarantee exactly-once delivery: even if multiple instances
// accidentally subscribe to the same stock's subject, NATS will route each
// order to only one of them.
//
// Subject pattern:  engine.orders.<stockCode>
// Queue group name: engine-<stockCode>
func wireEngineNATSOrders(
	sub *natsbroker.Subscriber,
	eng *engine.Engine,
	p *pkgpartition.Partitioner,
	allStocks []entity.Stock,
) {
	owned := p.OwnedStocks(stockCodes(allStocks))
	for _, stockCode := range owned {
		sc := stockCode // capture loop variable
		subject := "engine.orders." + sc
		queue := "engine-" + sc
		if err := sub.QueueSubscribe(subject, queue, func(data []byte) {
			var o entity.Order
			if err := json.Unmarshal(data, &o); err != nil {
				slog.Warn("engine nats: invalid order payload",
					"subject", subject, "error", err)
				return
			}
			if err := eng.SubmitOrder(&o); err != nil {
				slog.Warn("engine nats: submit failed",
					"stock", o.StockCode, "error", err)
			}
		}); err != nil {
			slog.Warn("nats engine queue subscribe failed",
				"subject", subject, "error", err)
		} else {
			slog.Info("nats engine queue subscriber wired",
				"subject", subject, "queue", queue)
		}
	}
}

// wireRedisPubSubSubscriber subscribes to Redis channels for market events
// and forwards decoded payloads to the local WebSocket hub.
// This is the fallback cross-instance broadcast used when NATS is disabled.
func wireRedisPubSubSubscriber(
	ctx context.Context,
	ps *redistore.PubSub,
	hub *ws.Hub,
	allStocks []entity.Stock,
) {
	for _, s := range allStocks {
		stock := s.Code // capture

		ps.Subscribe(ctx, "trade."+stock, func(data []byte) {
			var t entity.Trade
			if err := json.Unmarshal(data, &t); err == nil {
				hub.Broadcast("market.trade", stock, &t)
			}
		})
		ps.Subscribe(ctx, "ticker."+stock, func(data []byte) {
			var t entity.Ticker
			if err := json.Unmarshal(data, &t); err == nil {
				hub.Broadcast("market.ticker", stock, &t)
			}
		})
		ps.Subscribe(ctx, "orderbook."+stock, func(data []byte) {
			var b entity.OrderBook
			if err := json.Unmarshal(data, &b); err == nil {
				hub.Broadcast("market.orderbook", stock, &b)
			}
		})
	}
	// User-scoped order updates use a shared channel; userID is inside the payload.
	ps.Subscribe(ctx, "order.updates", func(data []byte) {
		var o entity.Order
		if err := json.Unmarshal(data, &o); err == nil {
			hub.BroadcastToUser("order.update", o.UserID, &o)
		}
	})
	slog.Info("redis pubsub subscriber wired", "stocks", len(allStocks))
}

// subjectSuffix returns the part after the first dot in a NATS subject.
// E.g. "trade.BBCA" → "BBCA", "order.user-123" → "user-123".
func subjectSuffix(subject string) string {
	idx := strings.Index(subject, ".")
	if idx < 0 || idx == len(subject)-1 {
		return subject
	}
	return subject[idx+1:]
}

// stockCodes extracts the Code field from a slice of Stock entities.
func stockCodes(stocks []entity.Stock) []string {
	codes := make([]string, len(stocks))
	for i, s := range stocks {
		codes[i] = s.Code
	}
	return codes
}

// filterStocks returns only those stocks whose Code appears in the allowed set.
func filterStocks(all []entity.Stock, allowed []string) []entity.Stock {
	set := make(map[string]struct{}, len(allowed))
	for _, c := range allowed {
		set[c] = struct{}{}
	}
	out := make([]entity.Stock, 0, len(allowed))
	for _, s := range all {
		if _, ok := set[s.Code]; ok {
			out = append(out, s)
		}
	}
	return out
}

// setupLogger configures the global slog logger based on the log config.
func setupLogger(level, format string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}
