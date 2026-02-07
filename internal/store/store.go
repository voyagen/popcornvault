package store

import (
	"context"
	"errors"

	"github.com/voyagen/popcornvault/internal/models"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// Store defines persistence for sources, channels, groups, and channel headers.
type Store interface {
	// CreateOrGetSource creates a source by name/url if not exists, returns id.
	CreateOrGetSource(ctx context.Context, name, url string, sourceType int16, userAgent string) (int64, error)
	// GetOrCreateGroup returns group id for name/sourceID, creating the group if needed.
	GetOrCreateGroup(ctx context.Context, sourceID int64, name string, image *string) (int64, error)
	// UpsertChannel inserts or updates a channel; returns channel id.
	UpsertChannel(ctx context.Context, ch *models.Channel) (int64, error)
	// UpsertChannelHeaders inserts or ignores headers for a channel.
	UpsertChannelHeaders(ctx context.Context, channelID int64, h *models.ChannelHttpHeaders) error
	// RemoveStaleChannels deletes channels (and their headers) for the source that are NOT in keepIDs.
	// Returns the number of deleted channels.
	RemoveStaleChannels(ctx context.Context, sourceID int64, keepIDs []int64) (int64, error)
	// RemoveOrphanedGroups deletes groups for the source that have no remaining channels.
	// Returns the number of deleted groups.
	RemoveOrphanedGroups(ctx context.Context, sourceID int64) (int64, error)
	// UpdateSourceLastUpdated sets last_updated for the source.
	UpdateSourceLastUpdated(ctx context.Context, sourceID int64) error

	// ListSources returns all sources.
	ListSources(ctx context.Context) ([]models.Source, error)
	// GetSourceByID returns a single source by id.
	GetSourceByID(ctx context.Context, sourceID int64) (*models.Source, error)

	// UpdateSource updates mutable fields of a source.
	UpdateSource(ctx context.Context, sourceID int64, fields SourceUpdate) error
	// DeleteSource deletes a source and cascades to channels/groups (via ON DELETE CASCADE).
	DeleteSource(ctx context.Context, sourceID int64) error

	// GetChannelByID returns a single channel by id (with group name joined).
	GetChannelByID(ctx context.Context, channelID int64) (*models.Channel, error)
	// ListChannels returns channels matching the filter and the total count (before limit/offset).
	ListChannels(ctx context.Context, filter ChannelFilter) ([]models.Channel, int, error)
	// ListGroups returns groups, optionally filtered by source id.
	ListGroups(ctx context.Context, sourceID *int64) ([]models.Group, error)

	// ToggleChannelFavorite sets the favorite flag on a channel.
	ToggleChannelFavorite(ctx context.Context, channelID int64, favorite bool) error
	// CountChannelsBySource returns the total number of channels for a source.
	CountChannelsBySource(ctx context.Context, sourceID int64) (int64, error)

	// StoreEmbeddings batch-updates the embedding column for the given channel IDs.
	StoreEmbeddings(ctx context.Context, channelIDs []int64, embeddings [][]float32) error
	// SemanticSearch returns channels ordered by cosine similarity to queryVec.
	SemanticSearch(ctx context.Context, queryVec []float32, filter ChannelFilter) ([]SemanticResult, error)
	// ListChannelsWithoutEmbeddings returns channels for a source that have no embedding yet.
	ListChannelsWithoutEmbeddings(ctx context.Context, sourceID int64, limit int) ([]models.Channel, error)
}

// SemanticResult wraps a Channel with its cosine similarity score.
type SemanticResult struct {
	Channel    models.Channel `json:"channel"`
	Similarity float64        `json:"similarity"`
}

// ChannelFilter holds optional filters for listing channels.
type ChannelFilter struct {
	SourceID  *int64
	GroupID   *int64
	MediaType *int16 // 0 = Livestream, 1 = Movie, 2 = Serie
	Favorite  *bool  // filter by favorite status
	Search    string // case-insensitive substring match on channel name
	Limit     int    // default 50, max 200
	Offset    int
}

// SourceUpdate holds mutable fields for PATCH /sources/{id}.
// Pointer fields: nil = don't change, non-nil = set.
type SourceUpdate struct {
	Name      *string
	URL       *string
	UserAgent *string
	Enabled   *bool
}
