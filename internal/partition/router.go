// Package partition provides the PartitionedRouter that routes order submission
// to the local matching engine (when this instance owns the stock) or forwards
// it to the owning instance via NATS.
//
// Architecture:
//
//	REST request arrives on any instance
//	  → Router.SubmitOrder(order)
//	      ├─ owns stock? → local engine (zero-latency, no network hop)
//	      └─ remote?    → NATS "engine.orders.<stockCode>"
//	                        → owning instance's QueueSubscribe handler
//	                            → local engine on owning instance
package partition

import (
	"fmt"

	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/pkg/partition"
)

// OrderSubmitter submits an order to a matching engine.
// *engine.Engine satisfies this interface.
type OrderSubmitter interface {
	SubmitOrder(order *entity.Order) error
}

// Publisher publishes a payload to a NATS subject.
// *natsbroker.Publisher satisfies this interface.
type Publisher interface {
	Publish(subject string, payload interface{}) error
}

// Router routes each SubmitOrder call either to the local engine (when this
// instance owns the stock) or to NATS (so the owning instance can pick it up).
type Router struct {
	local       OrderSubmitter
	publisher   Publisher
	partitioner *partition.Partitioner
}

// NewRouter creates a Router.
//   - local:       the local *engine.Engine
//   - publisher:   NATS publisher for forwarding remote orders
//   - partitioner: consistent-hash partitioner for this instance
func NewRouter(local OrderSubmitter, publisher Publisher, p *partition.Partitioner) *Router {
	return &Router{
		local:       local,
		publisher:   publisher,
		partitioner: p,
	}
}

// SubmitOrder routes the order:
//   - owned stock  → local engine (direct, no serialisation)
//   - remote stock → NATS subject "engine.orders.<stockCode>"
//     (the owning instance's QueueSubscribe handler will pick it up)
func (r *Router) SubmitOrder(order *entity.Order) error {
	if r.partitioner.OwnsStock(order.StockCode) {
		return r.local.SubmitOrder(order)
	}
	subject := "engine.orders." + order.StockCode
	if err := r.publisher.Publish(subject, order); err != nil {
		return fmt.Errorf("partition router: forward to %s: %w", subject, err)
	}
	return nil
}

// OwnsStock is a convenience pass-through to the underlying Partitioner.
func (r *Router) OwnsStock(stockCode string) bool {
	return r.partitioner.OwnsStock(stockCode)
}
