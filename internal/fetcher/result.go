package fetcher

import "github.com/voyagen/popcornvault/internal/models"

// ParsedEntry is a channel plus optional HTTP headers (from EXTVLCOPT).
type ParsedEntry struct {
	Channel models.Channel
	Headers *models.ChannelHttpHeaders
}
