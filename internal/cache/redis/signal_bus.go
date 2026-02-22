package redis

import (
	"context"
	"fmt"
	"strings"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/redis/go-redis/v9"
)

// streamMaxLen is the approximate maximum length for Redis streams, enforced
// via XADD MAXLEN ~.
const streamMaxLen int64 = 10000

// SignalBus implements domain.SignalBus using Redis Pub/Sub for ephemeral
// messaging and Redis Streams for durable, ordered message delivery.
type SignalBus struct {
	rdb *redis.Client
}

// NewSignalBus creates a SignalBus backed by the given Client.
func NewSignalBus(c *Client) *SignalBus {
	return &SignalBus{rdb: c.Underlying()}
}

// Publish sends a raw byte payload to a Redis Pub/Sub channel.
func (sb *SignalBus) Publish(ctx context.Context, channel string, payload []byte) error {
	if err := sb.rdb.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("redis: publish %s: %w", channel, err)
	}
	return nil
}

// Subscribe creates a Redis Pub/Sub subscription and returns a read-only
// channel that emits raw byte payloads. The subscription is automatically
// closed when the context is cancelled; the returned channel is closed at
// that point as well.
func (sb *SignalBus) Subscribe(ctx context.Context, channel string) (<-chan []byte, error) {
	var pubsub *redis.PubSub
	if hasPattern(channel) {
		pubsub = sb.rdb.PSubscribe(ctx, channel)
	} else {
		pubsub = sb.rdb.Subscribe(ctx, channel)
	}

	// Verify the subscription is established by receiving the confirmation.
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, fmt.Errorf("redis: subscribe %s: %w", channel, err)
	}

	out := make(chan []byte, 128)
	go func() {
		defer close(out)
		defer pubsub.Close()

		ch := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- []byte(msg.Payload):
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

// hasPattern returns true when the Redis channel includes glob-style
// wildcards, in which case PSubscribe must be used instead of Subscribe.
func hasPattern(channel string) bool {
	return strings.ContainsAny(channel, "*?[")
}

// StreamAppend appends a payload to a Redis stream using XADD with an
// approximate MAXLEN of 10,000 entries for automatic trimming.
func (sb *SignalBus) StreamAppend(ctx context.Context, stream string, payload []byte) error {
	args := &redis.XAddArgs{
		Stream: stream,
		MaxLen: streamMaxLen,
		Approx: true,
		Values: map[string]interface{}{
			"payload": payload,
		},
	}
	if err := sb.rdb.XAdd(ctx, args).Err(); err != nil {
		return fmt.Errorf("redis: stream append %s: %w", stream, err)
	}
	return nil
}

// StreamRead reads up to count messages from a Redis stream starting after
// lastID. Use "0" or "0-0" as lastID to read from the beginning, or "$" to
// read only new messages. It returns an empty slice (not an error) when no
// messages are available.
func (sb *SignalBus) StreamRead(ctx context.Context, stream string, lastID string, count int) ([]domain.StreamMessage, error) {
	args := &redis.XReadArgs{
		Streams: []string{stream, lastID},
		Count:   int64(count),
	}

	results, err := sb.rdb.XRead(ctx, args).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("redis: stream read %s: %w", stream, err)
	}

	var messages []domain.StreamMessage
	for _, s := range results {
		for _, msg := range s.Messages {
			payload, ok := msg.Values["payload"]
			if !ok {
				continue
			}

			var data []byte
			switch v := payload.(type) {
			case string:
				data = []byte(v)
			case []byte:
				data = v
			default:
				continue
			}

			messages = append(messages, domain.StreamMessage{
				ID:      msg.ID,
				Payload: data,
			})
		}
	}

	return messages, nil
}

// Compile-time interface check.
var _ domain.SignalBus = (*SignalBus)(nil)
