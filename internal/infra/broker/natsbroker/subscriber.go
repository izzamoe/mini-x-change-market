package natsbroker

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
)

// Subscriber listens to NATS subjects and routes messages to handlers.
type Subscriber struct {
	conn          *nats.Conn
	subscriptions []*nats.Subscription
}

// NewSubscriber connects to NATS at url and returns a Subscriber.
func NewSubscriber(url string) (*Subscriber, error) {
	conn, err := nats.Connect(url,
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			slog.Warn("nats subscriber disconnected", "error", err)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	return &Subscriber{conn: conn}, nil
}

// Subscribe registers handler to receive JSON-encoded messages on subject.
// The raw bytes are passed to handler for decoding into the appropriate type.
func (s *Subscriber) Subscribe(subject string, handler func([]byte)) error {
	sub, err := s.conn.Subscribe(subject, func(msg *nats.Msg) {
		handler(msg.Data)
	})
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	s.subscriptions = append(s.subscriptions, sub)
	return nil
}

// SubscribeSubject registers handler to receive messages on subject (supports
// wildcards such as "trade.*"). The handler receives both the concrete NATS
// subject of the message (e.g. "trade.BBCA") and the raw payload bytes.
// This is used for cross-instance WebSocket fan-out via NATS.
func (s *Subscriber) SubscribeSubject(subject string, handler func(subject string, data []byte)) error {
	sub, err := s.conn.Subscribe(subject, func(msg *nats.Msg) {
		handler(msg.Subject, msg.Data)
	})
	if err != nil {
		return fmt.Errorf("subscribe subject %s: %w", subject, err)
	}
	s.subscriptions = append(s.subscriptions, sub)
	return nil
}

// SubscribeJSON registers handler to receive JSON-decoded messages on subject.
func (s *Subscriber) SubscribeJSON(subject string, dst func([]byte)) error {
	return s.Subscribe(subject, dst)
}

// QueueSubscribe registers a queue-group subscription on subject.
// NATS delivers each incoming message to exactly ONE subscriber within the
// named queue group, providing automatic load-balancing and failover across
// instances.
//
// This is used by the matching-engine NATS receiver so that, even if
// multiple instances subscribe to the same stock's subject, each order is
// processed by exactly one matching engine — eliminating duplicates.
func (s *Subscriber) QueueSubscribe(subject, queue string, handler func([]byte)) error {
	sub, err := s.conn.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
		handler(msg.Data)
	})
	if err != nil {
		return fmt.Errorf("queue subscribe %s [%s]: %w", subject, queue, err)
	}
	s.subscriptions = append(s.subscriptions, sub)
	slog.Debug("nats queue subscribed", "subject", subject, "queue", queue)
	return nil
}

// MarshalAndForward is a helper that marshals a concrete value then calls handler.
func MarshalAndForward(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// Close drains and closes the NATS connection.
func (s *Subscriber) Close() {
	for _, sub := range s.subscriptions {
		_ = sub.Unsubscribe()
	}
	_ = s.conn.Drain()
}
