package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis wraps a go-redis client with convenience helpers for JSON
// serialisation, pattern deletion, and health checks.
type Redis struct {
	client *redis.Client
}

// New parses a Redis URL (e.g. "redis://host:6379/0") and returns a
// connected client. Call Ping to verify the connection.
func New(rawURL string) (*Redis, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	return &Redis{client: redis.NewClient(opts)}, nil
}

// Ping checks the connection to Redis.
func (r *Redis) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Close shuts down the Redis client.
func (r *Redis) Close() error {
	return r.client.Close()
}

// Client returns the underlying go-redis client for direct access.
func (r *Redis) Client() *redis.Client {
	return r.client
}

// --- generic JSON helpers ---

// Get fetches a key and JSON-unmarshals the value into dst.
// Returns redis.Nil when the key does not exist.
func Get[T any](ctx context.Context, r *Redis, key string) (T, error) {
	var zero T
	raw, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, fmt.Errorf("cache unmarshal %s: %w", key, err)
	}
	return v, nil
}

// Set JSON-marshals v and stores it under key with the given TTL.
func Set(ctx context.Context, r *Redis, key string, v any, ttl time.Duration) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("cache marshal %s: %w", key, err)
	}
	return r.client.Set(ctx, key, data, ttl).Err()
}

// Del deletes one or more exact keys.
func Del(ctx context.Context, r *Redis, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return r.client.Del(ctx, keys...).Err()
}

// DelPattern deletes all keys matching a glob pattern (e.g. "channels:*").
// Uses SCAN so it is safe for production, unlike KEYS.
func DelPattern(ctx context.Context, r *Redis, pattern string) error {
	var cursor uint64
	for {
		keys, next, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("cache scan %s: %w", pattern, err)
		}
		if len(keys) > 0 {
			if err := r.client.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("cache del pattern %s: %w", pattern, err)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return nil
}
