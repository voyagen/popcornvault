package store

import (
	"context"

	"github.com/voyagen/popcornvault/internal/models"
)

// Store defines persistence for sources, channels, groups, and channel headers.
type Store interface {
	// CreateOrGetSource creates a source by name/url if not exists, returns id.
	CreateOrGetSource(ctx context.Context, name, url string, sourceType int16, userAgent string) (int64, error)
	// WipeSourceChannels deletes all channels (and their headers) and group membership for the source.
	WipeSourceChannels(ctx context.Context, sourceID int64) error
	// GetOrCreateGroup returns group id for name/sourceID, creating the group if needed.
	GetOrCreateGroup(ctx context.Context, sourceID int64, name string, image *string) (int64, error)
	// UpsertChannel inserts or updates a channel; returns channel id.
	UpsertChannel(ctx context.Context, ch *models.Channel) (int64, error)
	// UpsertChannelHeaders inserts or ignores headers for a channel.
	UpsertChannelHeaders(ctx context.Context, channelID int64, h *models.ChannelHttpHeaders) error
	// UpdateSourceLastUpdated sets last_updated for the source.
	UpdateSourceLastUpdated(ctx context.Context, sourceID int64) error

	// ListSources returns all sources.
	ListSources(ctx context.Context) ([]models.Source, error)
	// GetSourceByID returns a single source by id.
	GetSourceByID(ctx context.Context, sourceID int64) (*models.Source, error)

	// ListChannels returns channels matching the filter and the total count (before limit/offset).
	ListChannels(ctx context.Context, filter ChannelFilter) ([]models.Channel, int, error)
	// ListGroups returns groups, optionally filtered by source id.
	ListGroups(ctx context.Context, sourceID *int64) ([]models.Group, error)
}

// ChannelFilter holds optional filters for listing channels.
type ChannelFilter struct {
	SourceID *int64
	GroupID  *int64
	Search   string // case-insensitive substring match on channel name
	Limit    int    // default 50, max 200
	Offset   int
}
