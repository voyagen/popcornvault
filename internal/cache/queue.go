package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// EmbeddingJob describes a background embedding generation task.
type EmbeddingJob struct {
	SourceID       int64   `json:"source_id"`
	SourceName     string  `json:"source_name"`
	ChannelIDs     []int64 `json:"channel_ids,omitempty"`
	EmbeddingsOnly bool    `json:"embeddings_only"`
}

// DefaultQueue is the Redis list key used for the embedding job queue.
const DefaultQueue = "popcornvault:jobs:embeddings"

// Enqueue pushes a job onto the left side of a Redis list.
func Enqueue(ctx context.Context, r *Redis, queue string, job EmbeddingJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("queue marshal: %w", err)
	}
	return r.client.LPush(ctx, queue, data).Err()
}

// Dequeue blocks until a job is available on the right side of the list
// or the timeout expires. When the timeout elapses without a job,
// (nil, nil) is returned so the caller can loop and check for shutdown.
func Dequeue(ctx context.Context, r *Redis, queue string, timeout time.Duration) (*EmbeddingJob, error) {
	result, err := r.client.BRPop(ctx, timeout, queue).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // timeout, no job available
		}
		// Context cancelled (shutdown) — not an error.
		if ctx.Err() != nil {
			return nil, nil
		}
		return nil, fmt.Errorf("queue dequeue: %w", err)
	}
	// BRPop returns [key, value].
	if len(result) < 2 {
		return nil, nil
	}
	var job EmbeddingJob
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return nil, fmt.Errorf("queue unmarshal: %w", err)
	}
	return &job, nil
}
