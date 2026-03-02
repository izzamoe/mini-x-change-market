package broker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/izzam/mini-exchange/internal/domain/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEvent(t event.Type, stock string) event.Event {
	return event.Event{
		Type:      t,
		StockCode: stock,
		Timestamp: time.Now(),
	}
}

// startAndStop is a helper that starts the bus, runs fn, then stops and
// waits for all deliveries to finish.
func startAndStop(bus *EventBus, fn func()) {
	bus.Start()
	fn()
	bus.Stop()
}

func TestEventBus_SubscribeAndReceive(t *testing.T) {
	bus := NewEventBus()

	var received []event.Event
	var mu sync.Mutex

	bus.Subscribe(event.TradeExecuted, func(e event.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})

	startAndStop(bus, func() {
		bus.Publish(makeEvent(event.TradeExecuted, "BBCA"))
		bus.Publish(makeEvent(event.TradeExecuted, "BBRI"))
	})

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, received, 2)
}

func TestEventBus_NoSubscribers(t *testing.T) {
	bus := NewEventBus()
	// Publishing with no subscribers should not panic or block.
	startAndStop(bus, func() {
		bus.Publish(makeEvent(event.TradeExecuted, "BBCA"))
	})
}

func TestEventBus_FanOut_MultipleSubscribers(t *testing.T) {
	bus := NewEventBus()

	const n = 5
	counters := make([]int32, n)

	for i := 0; i < n; i++ {
		idx := i
		bus.Subscribe(event.TickerUpdated, func(e event.Event) {
			atomic.AddInt32(&counters[idx], 1)
		})
	}

	startAndStop(bus, func() {
		bus.Publish(makeEvent(event.TickerUpdated, "BBCA"))
	})

	for i, c := range counters {
		assert.EqualValues(t, 1, c, "subscriber %d did not receive the event", i)
	}
}

func TestEventBus_EventTypeIsolation(t *testing.T) {
	bus := NewEventBus()

	var tradeCount, tickerCount int32

	bus.Subscribe(event.TradeExecuted, func(e event.Event) {
		atomic.AddInt32(&tradeCount, 1)
	})
	bus.Subscribe(event.TickerUpdated, func(e event.Event) {
		atomic.AddInt32(&tickerCount, 1)
	})

	startAndStop(bus, func() {
		bus.Publish(makeEvent(event.TradeExecuted, "BBCA"))
		bus.Publish(makeEvent(event.TradeExecuted, "BBCA"))
		bus.Publish(makeEvent(event.TickerUpdated, "BBCA"))
	})

	assert.EqualValues(t, 2, atomic.LoadInt32(&tradeCount))
	assert.EqualValues(t, 1, atomic.LoadInt32(&tickerCount))
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := NewEventBus()

	var count int32
	id := bus.Subscribe(event.OrderCreated, func(e event.Event) {
		atomic.AddInt32(&count, 1)
	})

	bus.Unsubscribe(id)

	startAndStop(bus, func() {
		bus.Publish(makeEvent(event.OrderCreated, "BBCA"))
	})

	assert.EqualValues(t, 0, atomic.LoadInt32(&count))
}

func TestEventBus_NonBlocking_FullBuffer(t *testing.T) {
	bus := NewEventBus()
	// Do NOT start the bus — the dispatch goroutine is not running, so the
	// channel will fill up. Publish must return immediately without blocking.

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < dispatchBufferSize+100; i++ {
			bus.Publish(makeEvent(event.TradeExecuted, "BBCA"))
		}
	}()

	select {
	case <-done:
		// Passed: all publishes returned quickly.
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked when buffer was full")
	}
}

func TestEventBus_Start_Idempotent(t *testing.T) {
	bus := NewEventBus()
	// Calling Start multiple times should not panic or launch extra goroutines.
	bus.Start()
	bus.Start()
	bus.Start()
	bus.Stop()
}

func TestEventBus_ConcurrentPublishSubscribe(t *testing.T) {
	bus := NewEventBus()

	var total int64
	bus.Subscribe(event.OrderUpdated, func(e event.Event) {
		atomic.AddInt64(&total, 1)
	})
	bus.Start()

	const goroutines = 50
	const eventsPerGoroutine = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				bus.Publish(makeEvent(event.OrderUpdated, "BBCA"))
			}
		}()
	}
	wg.Wait()

	bus.Stop()

	// All 5000 events must have been delivered (buffer is 10000, so no drops).
	require.EqualValues(t, goroutines*eventsPerGoroutine, atomic.LoadInt64(&total))
}

func TestEventBus_HandlerCanUnsubscribeWithoutDeadlock(t *testing.T) {
	bus := NewEventBus()

	var id string
	var called int32

	id = bus.Subscribe(event.OrderBookUpdated, func(e event.Event) {
		atomic.AddInt32(&called, 1)
		bus.Unsubscribe(id) // must not deadlock
	})

	startAndStop(bus, func() {
		bus.Publish(makeEvent(event.OrderBookUpdated, "BBCA"))
		bus.Publish(makeEvent(event.OrderBookUpdated, "BBCA")) // second event: handler already removed
	})

	assert.EqualValues(t, 1, atomic.LoadInt32(&called))
}
