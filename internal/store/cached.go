package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/voyagen/popcornvault/internal/cache"
	"github.com/voyagen/popcornvault/internal/models"
)

// Cache TTLs for different entity types.
const (
	ttlSources  = 2 * time.Minute
	ttlSource   = 5 * time.Minute
	ttlChannels = 1 * time.Minute
	ttlChannel  = 5 * time.Minute
	ttlGroups   = 5 * time.Minute
	ttlSearch   = 2 * time.Minute
)

// CachedStore wraps a Store with a Redis caching layer.
// Read-heavy operations are served from cache when possible;
// write operations invalidate the relevant cache keys.
type CachedStore struct {
	inner Store
	cache *cache.Redis
}

// NewCachedStore creates a CachedStore that wraps inner with Redis caching.
func NewCachedStore(inner Store, c *cache.Redis) *CachedStore {
	return &CachedStore{inner: inner, cache: c}
}

// --- cached read operations ---

func (c *CachedStore) ListSources(ctx context.Context) ([]models.Source, error) {
	const key = "sources:all"
	if v, err := cache.Get[[]models.Source](ctx, c.cache, key); err == nil {
		return v, nil
	}
	sources, err := c.inner.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Set(ctx, c.cache, key, sources, ttlSources); err != nil {
		log.Printf("cache: set %s: %v", key, err)
	}
	return sources, nil
}

func (c *CachedStore) GetSourceByID(ctx context.Context, sourceID int64) (*models.Source, error) {
	key := fmt.Sprintf("source:%d", sourceID)
	if v, err := cache.Get[models.Source](ctx, c.cache, key); err == nil {
		return &v, nil
	}
	src, err := c.inner.GetSourceByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if err := cache.Set(ctx, c.cache, key, src, ttlSource); err != nil {
		log.Printf("cache: set %s: %v", key, err)
	}
	return src, nil
}

// channelListResult is a helper type to cache the ListChannels tuple.
type channelListResult struct {
	Channels []models.Channel `json:"channels"`
	Total    int              `json:"total"`
}

func (c *CachedStore) ListChannels(ctx context.Context, filter ChannelFilter) ([]models.Channel, int, error) {
	key := fmt.Sprintf("channels:%s", filterHash(filter))
	if v, err := cache.Get[channelListResult](ctx, c.cache, key); err == nil {
		return v.Channels, v.Total, nil
	}
	channels, total, err := c.inner.ListChannels(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	if err := cache.Set(ctx, c.cache, key, channelListResult{Channels: channels, Total: total}, ttlChannels); err != nil {
		log.Printf("cache: set %s: %v", key, err)
	}
	return channels, total, nil
}

func (c *CachedStore) GetChannelByID(ctx context.Context, channelID int64) (*models.Channel, error) {
	key := fmt.Sprintf("channel:%d", channelID)
	if v, err := cache.Get[models.Channel](ctx, c.cache, key); err == nil {
		return &v, nil
	}
	ch, err := c.inner.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if err := cache.Set(ctx, c.cache, key, ch, ttlChannel); err != nil {
		log.Printf("cache: set %s: %v", key, err)
	}
	return ch, nil
}

func (c *CachedStore) ListGroups(ctx context.Context, sourceID *int64) ([]models.Group, error) {
	sid := "all"
	if sourceID != nil {
		sid = fmt.Sprintf("%d", *sourceID)
	}
	key := fmt.Sprintf("groups:%s", sid)
	if v, err := cache.Get[[]models.Group](ctx, c.cache, key); err == nil {
		return v, nil
	}
	groups, err := c.inner.ListGroups(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if err := cache.Set(ctx, c.cache, key, groups, ttlGroups); err != nil {
		log.Printf("cache: set %s: %v", key, err)
	}
	return groups, nil
}

// semanticSearchResult caches the SemanticSearch return value.
type semanticSearchResult struct {
	Results []SemanticResult `json:"results"`
}

func (c *CachedStore) SemanticSearch(ctx context.Context, queryVec []float32, filter ChannelFilter) ([]SemanticResult, error) {
	key := fmt.Sprintf("search:%s:%s", vecHash(queryVec), filterHash(filter))
	if v, err := cache.Get[semanticSearchResult](ctx, c.cache, key); err == nil {
		return v.Results, nil
	}
	results, err := c.inner.SemanticSearch(ctx, queryVec, filter)
	if err != nil {
		return nil, err
	}
	if err := cache.Set(ctx, c.cache, key, semanticSearchResult{Results: results}, ttlSearch); err != nil {
		log.Printf("cache: set %s: %v", key, err)
	}
	return results, nil
}

// --- write operations with cache invalidation ---

func (c *CachedStore) CreateOrGetSource(ctx context.Context, name, url string, sourceType int16, userAgent string) (int64, error) {
	id, err := c.inner.CreateOrGetSource(ctx, name, url, sourceType, userAgent)
	if err != nil {
		return 0, err
	}
	c.invalidate(ctx, "sources:all")
	return id, nil
}

func (c *CachedStore) UpdateSource(ctx context.Context, sourceID int64, fields SourceUpdate) error {
	if err := c.inner.UpdateSource(ctx, sourceID, fields); err != nil {
		return err
	}
	c.invalidate(ctx, fmt.Sprintf("source:%d", sourceID), "sources:all")
	return nil
}

func (c *CachedStore) DeleteSource(ctx context.Context, sourceID int64) error {
	if err := c.inner.DeleteSource(ctx, sourceID); err != nil {
		return err
	}
	c.invalidate(ctx, fmt.Sprintf("source:%d", sourceID), "sources:all")
	c.invalidatePattern(ctx, "channels:*", "groups:*", "search:*")
	return nil
}

func (c *CachedStore) UpdateSourceLastUpdated(ctx context.Context, sourceID int64) error {
	if err := c.inner.UpdateSourceLastUpdated(ctx, sourceID); err != nil {
		return err
	}
	c.invalidate(ctx, fmt.Sprintf("source:%d", sourceID), "sources:all")
	return nil
}

func (c *CachedStore) UpsertChannel(ctx context.Context, ch *models.Channel) (int64, error) {
	id, err := c.inner.UpsertChannel(ctx, ch)
	if err != nil {
		return 0, err
	}
	// Individual channel caches and list caches may be stale.
	c.invalidate(ctx, fmt.Sprintf("channel:%d", id))
	c.invalidatePattern(ctx, "channels:*")
	return id, nil
}

func (c *CachedStore) UpsertChannelHeaders(ctx context.Context, channelID int64, h *models.ChannelHttpHeaders) error {
	return c.inner.UpsertChannelHeaders(ctx, channelID, h)
}

func (c *CachedStore) ToggleChannelFavorite(ctx context.Context, channelID int64, favorite bool) error {
	if err := c.inner.ToggleChannelFavorite(ctx, channelID, favorite); err != nil {
		return err
	}
	c.invalidate(ctx, fmt.Sprintf("channel:%d", channelID))
	c.invalidatePattern(ctx, "channels:*")
	return nil
}

func (c *CachedStore) RemoveStaleChannels(ctx context.Context, sourceID int64, keepIDs []int64) (int64, error) {
	n, err := c.inner.RemoveStaleChannels(ctx, sourceID, keepIDs)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		c.invalidatePattern(ctx, "channels:*", "channel:*")
	}
	return n, nil
}

func (c *CachedStore) RemoveOrphanedGroups(ctx context.Context, sourceID int64) (int64, error) {
	n, err := c.inner.RemoveOrphanedGroups(ctx, sourceID)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		c.invalidatePattern(ctx, "groups:*")
	}
	return n, nil
}

func (c *CachedStore) StoreEmbeddings(ctx context.Context, channelIDs []int64, embeddings [][]float32) error {
	if err := c.inner.StoreEmbeddings(ctx, channelIDs, embeddings); err != nil {
		return err
	}
	c.invalidatePattern(ctx, "search:*")
	return nil
}

// --- passthrough (no caching) ---

func (c *CachedStore) GetOrCreateGroup(ctx context.Context, sourceID int64, name string, image *string) (int64, error) {
	return c.inner.GetOrCreateGroup(ctx, sourceID, name, image)
}

func (c *CachedStore) CountChannelsBySource(ctx context.Context, sourceID int64) (int64, error) {
	return c.inner.CountChannelsBySource(ctx, sourceID)
}

func (c *CachedStore) ListChannelsBySource(ctx context.Context, sourceID int64) ([]models.Channel, error) {
	return c.inner.ListChannelsBySource(ctx, sourceID)
}

func (c *CachedStore) ListChannelsWithoutEmbeddings(ctx context.Context, sourceID int64, limit int) ([]models.Channel, error) {
	return c.inner.ListChannelsWithoutEmbeddings(ctx, sourceID, limit)
}

// --- helpers ---

// invalidate deletes exact cache keys, logging any errors.
func (c *CachedStore) invalidate(ctx context.Context, keys ...string) {
	if err := cache.Del(ctx, c.cache, keys...); err != nil && err != redis.Nil {
		log.Printf("cache: del %v: %v", keys, err)
	}
}

// invalidatePattern deletes all keys matching the given glob patterns.
func (c *CachedStore) invalidatePattern(ctx context.Context, patterns ...string) {
	for _, p := range patterns {
		if err := cache.DelPattern(ctx, c.cache, p); err != nil {
			log.Printf("cache: del pattern %s: %v", p, err)
		}
	}
}

// filterHash produces a short deterministic hash for a ChannelFilter so it
// can be used as part of a cache key.
func filterHash(f ChannelFilter) string {
	raw := fmt.Sprintf("%v|%v|%v|%v|%s|%d|%d",
		f.SourceID, f.GroupID, f.MediaType, f.Favorite, f.Search, f.Limit, f.Offset)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:8])
}

// vecHash produces a short hash for a float32 vector.
func vecHash(v []float32) string {
	raw := fmt.Sprintf("%v", v)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:8])
}
