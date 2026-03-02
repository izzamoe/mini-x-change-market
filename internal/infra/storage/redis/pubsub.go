package redis

import (
	"context"
	"fmt"
	"log/slog"

	goredis "github.com/redis/go-redis/v9"
)

// PubSub provides Redis pub/sub for cross-instance WebSocket broadcasting.
// Each server instance subscribes to the relevant channels; when an event
// arrives from another instance it is forwarded to the local WS Hub.
type PubSub struct {
	client *goredis.Client
}

// NewPubSub creates a PubSub backed by the same Redis URL as NewCache.
func NewPubSub(redisURL string) (*PubSub, error) {
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redis parse url: %w", err)
	}
	return &PubSub{client: goredis.NewClient(opts)}, nil
}

// Publish marshals and sends data to channel.
func (p *PubSub) Publish(ctx context.Context, channel string, data []byte) error {
	return p.client.Publish(ctx, channel, data).Err()
}

// Subscribe starts a blocking subscription to channel in a goroutine.
// Each received message calls handler. The goroutine exits when ctx is
// cancelled.
func (p *PubSub) Subscribe(ctx context.Context, channel string, handler func([]byte)) {
	sub := p.client.Subscribe(ctx, channel)
	go func() {
		defer sub.Close()
		ch := sub.Channel()
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				handler([]byte(msg.Payload))
			case <-ctx.Done():
				return
			}
		}
	}()
	slog.Debug("redis pubsub subscribed", "channel", channel)
}

// Close closes the Redis client.
func (p *PubSub) Close() error {
	return p.client.Close()
}
