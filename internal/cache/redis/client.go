// Package redis implements domain cache interfaces using go-redis/v9.
package redis

import (
	"context"
	"crypto/tls"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// ClientConfig holds connection parameters for the Redis client.
type ClientConfig struct {
	Addr       string
	Password   string
	DB         int
	PoolSize   int
	MaxRetries int
	TLSEnabled bool
}

// Client wraps a go-redis Client and provides connectivity helpers.
type Client struct {
	rdb *redis.Client
}

// New creates a new Redis Client, pings it to verify connectivity, and returns
// the wrapper. It returns an error if the connection cannot be established.
func New(ctx context.Context, cfg ClientConfig) (*Client, error) {
	opts := &redis.Options{
		Addr:       cfg.Addr,
		Password:   cfg.Password,
		DB:         cfg.DB,
		PoolSize:   cfg.PoolSize,
		MaxRetries: cfg.MaxRetries,
	}

	if cfg.TLSEnabled {
		opts.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	rdb := redis.NewClient(opts)

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

// Ping checks the Redis connection.
func (c *Client) Ping(ctx context.Context) error {
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: ping: %w", err)
	}
	return nil
}

// Close closes the Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Underlying returns the raw *redis.Client for sub-packages that need direct
// access to the driver.
func (c *Client) Underlying() *redis.Client {
	return c.rdb
}
