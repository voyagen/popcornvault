package service

import (
	"context"
	"fmt"
	"time"

	"github.com/voyagen/popcornvault/internal/fetcher"
	"github.com/voyagen/popcornvault/internal/models"
	"github.com/voyagen/popcornvault/internal/store"
)

// Ingest fetches an M3U URL, parses it, and stores sources and channels.
// sourceName is optional; if empty, a default name is derived (e.g. from URL or "m3u").
func Ingest(ctx context.Context, s store.Store, m3uURL string, sourceName string, userAgent string, timeout time.Duration, useTvgID bool) (sourceID int64, channelCount int, err error) {
	if m3uURL == "" {
		return 0, 0, fmt.Errorf("m3u URL is required")
	}
	if sourceName == "" {
		sourceName = "m3u"
	}

	entries, err := fetcher.FetchM3U(ctx, m3uURL, userAgent, useTvgID, timeout)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch: %w", err)
	}

	sourceID, err = s.CreateOrGetSource(ctx, sourceName, m3uURL, models.SourceTypeM3ULink, userAgent)
	if err != nil {
		return 0, 0, fmt.Errorf("CreateOrGetSource: %w", err)
	}

	if err := s.WipeSourceChannels(ctx, sourceID); err != nil {
		return 0, 0, fmt.Errorf("WipeSourceChannels: %w", err)
	}

	groupIDs := make(map[string]int64)
	for i := range entries {
		ch := &entries[i].Channel
		ch.SourceID = sourceID

		if ch.Group != nil && *ch.Group != "" {
			gname := *ch.Group
			if gid, ok := groupIDs[gname]; ok {
				ch.GroupID = &gid
			} else {
				gid, err := s.GetOrCreateGroup(ctx, sourceID, gname, ch.Image)
				if err != nil {
					return 0, 0, fmt.Errorf("GetOrCreateGroup: %w", err)
				}
				groupIDs[gname] = gid
				ch.GroupID = &gid
			}
		}

		cid, err := s.UpsertChannel(ctx, ch)
		if err != nil {
			return 0, 0, fmt.Errorf("UpsertChannel: %w", err)
		}
		if entries[i].Headers != nil {
			if err := s.UpsertChannelHeaders(ctx, cid, entries[i].Headers); err != nil {
				return 0, 0, fmt.Errorf("UpsertChannelHeaders: %w", err)
			}
		}
		channelCount++
	}

	if err := s.UpdateSourceLastUpdated(ctx, sourceID); err != nil {
		return sourceID, channelCount, fmt.Errorf("UpdateSourceLastUpdated: %w", err)
	}
	return sourceID, channelCount, nil
}
