// Package broker provides in-process event bus implementations.
// For multi-node deployments, replace or augment with the NATS implementation
// in internal/infra/broker/nats/.
package broker

import (
	"sync"

	"github.com/google/uuid"
	"github.com/izzam/mini-exchange/internal/domain/event"
)

const (
	// dispatchBufferSize is the capacity of the internal dispatch channel.
	// A large buffer ensures Publish() never blocks even under burst traffic.
	dispatchBufferSize = 10_000
)

// subscription holds a single registered handler.
type subscription struct {
	id      string
	handler event.HandlerFunc
}

// EventBus is an in-process, fan-out event bus.
//
// Architecture:
//   - A single goroutine (started by Start) reads from the dispatch channel and
//     calls every registered handler for the event type in order.
//   - Publish is non-blocking: events are dropped (with a counter) if the
//     internal buffer is full, rather than blocking callers.
//   - Handlers are called synchronously inside the dispatch goroutine, so they
//     MUST NOT block. Wrap in a goroutine launch if async fan-out is needed.
type EventBus struct {
	mu   sync.RWMutex
	subs map[event.Type][]subscription

	ch   chan event.Event
	done chan struct{}
	once sync.Once
}

// NewEventBus constructs an EventBus. Call Start() before publishing events.
func NewEventBus() *EventBus {
	return &EventBus{
		subs: make(map[event.Type][]subscription),
		ch:   make(chan event.Event, dispatchBufferSize),
		done: make(chan struct{}),
	}
}

// Subscribe registers a handler for a specific event type and returns a
// subscription ID that can be passed to Unsubscribe.
func (b *EventBus) Subscribe(t event.Type, fn event.HandlerFunc) string {
	id := uuid.NewString()

	b.mu.Lock()
	b.subs[t] = append(b.subs[t], subscription{id: id, handler: fn})
	b.mu.Unlock()

	return id
}

// Unsubscribe removes the subscription with the given ID.
// It is safe to call from inside a handler.
func (b *EventBus) Unsubscribe(subscriptionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for t, list := range b.subs {
		for i, s := range list {
			if s.id == subscriptionID {
				b.subs[t] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
}

// Publish enqueues an event for dispatch. It is non-blocking: if the internal
// buffer is full the event is silently dropped (in production, a Prometheus
// counter would track drops; see the metrics middleware in plan-8).
func (b *EventBus) Publish(e event.Event) {
	select {
	case b.ch <- e:
	default:
		// Buffer full — drop the event rather than block the caller.
		// TODO(plan-8): increment eventbus_dropped_total Prometheus counter.
	}
}

// Start launches the dispatch goroutine. It returns immediately.
// Events published before Start is called are queued in the buffer.
// Safe to call only once; subsequent calls are no-ops.
func (b *EventBus) Start() {
	b.once.Do(func() {
		go b.dispatch()
	})
}

// Stop signals the dispatch goroutine to stop and drains remaining events.
// It blocks until all buffered events have been delivered.
func (b *EventBus) Stop() {
	close(b.ch)
	<-b.done
}

// dispatch is the single goroutine that delivers events to subscribers.
func (b *EventBus) dispatch() {
	defer close(b.done)

	for e := range b.ch {
		b.deliver(e)
	}
}

// deliver calls every handler registered for the event's type.
// The subscriber list is copied under a read lock so that handlers may
// themselves call Subscribe/Unsubscribe without deadlocking.
func (b *EventBus) deliver(e event.Event) {
	b.mu.RLock()
	handlers := make([]event.HandlerFunc, len(b.subs[e.Type]))
	for i, s := range b.subs[e.Type] {
		handlers[i] = s.handler
	}
	b.mu.RUnlock()

	for _, h := range handlers {
		h(e)
	}
}
