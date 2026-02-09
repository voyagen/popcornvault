package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrLocked is returned by TryLock when the lock is already held.
var ErrLocked = errors.New("lock is already held")

// TryLock attempts to acquire a distributed lock identified by key.
// It uses the Redis SET NX EX pattern. On success it returns an unlock
// function that MUST be called (typically via defer) to release the lock.
// If the lock is already held, ErrLocked is returned.
func TryLock(ctx context.Context, r *Redis, key string, ttl time.Duration) (unlock func(), err error) {
	// Random token ensures only the holder can release the lock.
	token := randomToken()

	ok, err := r.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("cache lock %s: %w", key, err)
	}
	if !ok {
		return nil, ErrLocked
	}

	// unlock deletes the key only if the token still matches (Lua script for atomicity).
	unlockScript := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		end
		return 0
	`
	return func() {
		// Use a background context so unlock works even if the request context is cancelled.
		_ = r.client.Eval(context.Background(), unlockScript, []string{key}, token).Err()
	}, nil
}

// IsLocked returns true if the lock key exists.
func IsLocked(ctx context.Context, r *Redis, key string) bool {
	n, _ := r.client.Exists(ctx, key).Result()
	return n > 0
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
