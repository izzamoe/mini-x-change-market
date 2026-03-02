// Package natsbroker provides NATS-based event publishing for horizontal scaling.
// When multiple server instances run behind a load balancer, domain events are
// published to NATS so every instance's WebSocket hub can broadcast to its
// locally connected clients.
package natsbroker

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
)

// Publisher publishes domain events to NATS subjects.
type Publisher struct {
	conn *nats.Conn
}

// NewPublisher dials NATS at url and returns a Publisher.
func NewPublisher(url string) (*Publisher, error) {
	conn, err := nats.Connect(url,
		nats.MaxReconnects(-1), // reconnect forever
		nats.ReconnectBufSize(16*1024*1024),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			slog.Warn("nats disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			slog.Info("nats reconnected")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	return &Publisher{conn: conn}, nil
}

// Publish marshals payload as JSON and publishes it to subject.
func (p *Publisher) Publish(subject string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return p.conn.Publish(subject, data)
}

// Close drains and closes the NATS connection.
func (p *Publisher) Close() {
	_ = p.conn.Drain()
}
