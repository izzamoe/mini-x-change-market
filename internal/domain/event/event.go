// Package event defines the domain events published by the trading system.
// Events flow from the core (matching engine, simulator) outward to subscribers
// (WebSocket hub, NATS publisher, metrics collector) via an EventBus.
package event

import "time"

// Type identifies which domain event occurred.
type Type string

const (
	// OrderCreated fires when a new order is persisted and queued for matching.
	OrderCreated Type = "order.created"

	// OrderUpdated fires when an order's status or filled quantity changes.
	OrderUpdated Type = "order.updated"

	// TradeExecuted fires when two orders are matched and a trade is created.
	TradeExecuted Type = "trade.executed"

	// TickerUpdated fires when the market snapshot for a stock changes.
	TickerUpdated Type = "ticker.updated"

	// OrderBookUpdated fires when the order book depth changes.
	OrderBookUpdated Type = "orderbook.updated"
)

// Event is the envelope published onto the EventBus.
// Payload carries the concrete domain object; consumers type-assert as needed.
type Event struct {
	Type      Type        // which event
	StockCode string      // affected stock (empty for user-scoped events)
	UserID    string      // affected user (empty for market-wide events)
	Payload   interface{} // *entity.Order, *entity.Trade, *entity.Ticker, etc.
	Timestamp time.Time
}

// HandlerFunc is a callback that processes a received event.
// Implementations must be non-blocking and goroutine-safe.
type HandlerFunc func(Event)

// Bus is the interface that all event bus implementations must satisfy.
type Bus interface {
	// Publish broadcasts an event to all registered subscribers.
	// It MUST be non-blocking; implementations buffer internally.
	Publish(e Event)

	// Subscribe registers a handler for a specific event type.
	// Returns a subscription ID that can be used with Unsubscribe.
	Subscribe(t Type, fn HandlerFunc) string

	// Unsubscribe removes the handler with the given subscription ID.
	Unsubscribe(subscriptionID string)

	// Start launches the internal dispatch goroutine.
	// It runs until the provided context is cancelled.
	Start()

	// Stop drains pending events and shuts down the dispatcher.
	Stop()
}
