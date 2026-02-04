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
}
