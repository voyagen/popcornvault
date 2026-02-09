package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/voyagen/popcornvault/internal/embedding"
	"github.com/voyagen/popcornvault/internal/fetcher"
	"github.com/voyagen/popcornvault/internal/models"
	"github.com/voyagen/popcornvault/internal/store"
)

// progressInterval controls how often the upsert loop logs progress.
const progressInterval = 5000

// Ingest fetches an M3U URL, parses it, and stores sources and channels.
// Existing channels are updated in place (preserving user data like favorites).
// Channels that no longer appear in the M3U are removed, and new ones are added.
// sourceName is optional; if empty, a default name is derived (e.g. from URL or "m3u").
// embedder is optional; if non-nil, embeddings are generated for ingested channels.
func Ingest(ctx context.Context, s store.Store, m3uURL string, sourceName string, userAgent string, timeout time.Duration, useTvgID bool, embedder ...*embedding.Client) (sourceID int64, channelCount int, err error) {
	if m3uURL == "" {
		return 0, 0, fmt.Errorf("m3u URL is required")
	}
	if sourceName == "" {
		sourceName = "m3u"
	}

	totalStart := time.Now()
	prefix := fmt.Sprintf("ingest[%s]", sourceName)

	// --- Phase 1: Fetch M3U ---
	log.Printf("%s: fetching M3U from %s ...", prefix, m3uURL)
	fetchStart := time.Now()

	entries, err := fetcher.FetchM3U(ctx, m3uURL, userAgent, useTvgID, timeout)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch: %w", err)
	}

	log.Printf("%s: fetched %d entries (%s)", prefix, len(entries), formatDur(time.Since(fetchStart)))

	sourceID, err = s.CreateOrGetSource(ctx, sourceName, m3uURL, models.SourceTypeM3ULink, userAgent)
	if err != nil {
		return 0, 0, fmt.Errorf("CreateOrGetSource: %w", err)
	}

	// --- Phase 2: Upsert channels ---
	log.Printf("%s: upserting channels ...", prefix)
	upsertStart := time.Now()

	keepIDs := make([]int64, 0, len(entries))
	groupIDs := make(map[string]int64)
	total := len(entries)

	for i := range entries {
		// Check for context cancellation between iterations to allow
		// graceful shutdown during long ingests.
		if err := ctx.Err(); err != nil {
			return sourceID, channelCount, fmt.Errorf("ingest cancelled: %w", err)
		}

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
		keepIDs = append(keepIDs, cid)

		if entries[i].Headers != nil {
			if err := s.UpsertChannelHeaders(ctx, cid, entries[i].Headers); err != nil {
				return 0, 0, fmt.Errorf("UpsertChannelHeaders: %w", err)
			}
		}
		channelCount++

		if channelCount%progressInterval == 0 {
			log.Printf("%s:   %d / %d channels upserted", prefix, channelCount, total)
		}
	}

	log.Printf("%s:   %d / %d channels upserted (%s)", prefix, channelCount, total, formatDur(time.Since(upsertStart)))

	// --- Phase 3: Cleanup ---
	cleanupStart := time.Now()

	// Pre-count to show expected stale channels before the slow DELETE.
	totalInDB, _ := s.CountChannelsBySource(ctx, sourceID)
	expectedStale := totalInDB - int64(len(keepIDs))
	if expectedStale < 0 {
		expectedStale = 0
	}

	log.Printf("%s: removing stale channels (~%d of %d in db) ...", prefix, expectedStale, totalInDB)
	staleStart := time.Now()

	staleCount, err := s.RemoveStaleChannels(ctx, sourceID, keepIDs)
	if err != nil {
		return sourceID, channelCount, fmt.Errorf("RemoveStaleChannels: %w", err)
	}

	log.Printf("%s: removed %d stale channels (%s)", prefix, staleCount, formatDur(time.Since(staleStart)))

	log.Printf("%s: removing orphaned groups ...", prefix)
	orphanStart := time.Now()

	orphanCount, err := s.RemoveOrphanedGroups(ctx, sourceID)
	if err != nil {
		return sourceID, channelCount, fmt.Errorf("RemoveOrphanedGroups: %w", err)
	}

	log.Printf("%s: removed %d orphaned groups (%s)", prefix, orphanCount, formatDur(time.Since(orphanStart)))
	log.Printf("%s: cleanup done (%s)", prefix, formatDur(time.Since(cleanupStart)))

	if err := s.UpdateSourceLastUpdated(ctx, sourceID); err != nil {
		return sourceID, channelCount, fmt.Errorf("UpdateSourceLastUpdated: %w", err)
	}

	log.Printf("%s: done -- %d channels ingested (%s)", prefix, channelCount, formatDur(time.Since(totalStart)))

	// --- Phase 4: Embeddings (background) ---
	// Run embedding generation in a background goroutine with a detached
	// context so it is not cancelled when the HTTP request completes.
	var embClient *embedding.Client
	if len(embedder) > 0 {
		embClient = embedder[0]
	}
	if embClient != nil && len(keepIDs) > 0 {
		// Copy what we need — the goroutine must not reference the request context.
		ids := make([]int64, len(keepIDs))
		copy(ids, keepIDs)
		entriesCopy := make([]fetcher.ParsedEntry, len(entries))
		copy(entriesCopy, entries)

		go func() {
			bgCtx := context.Background()
			if err := GenerateEmbeddings(bgCtx, s, embClient, ids, entriesCopy, prefix); err != nil {
				log.Printf("%s: warning: embedding generation failed: %v", prefix, err)
			}
		}()
		log.Printf("%s: embedding generation started in background (%d channels)", prefix, len(keepIDs))
	}
	return sourceID, channelCount, nil
}

// RefreshEmbeddings loads all channels for a source from the database and
// (re-)generates their embeddings. Embeddings are generated and stored one
// batch at a time to keep memory usage constant regardless of source size.
// Returns the number of channels that were embedded.
func RefreshEmbeddings(ctx context.Context, s store.Store, embClient *embedding.Client, sourceID int64, sourceName string) (int, error) {
	const batchSize = 128

	prefix := fmt.Sprintf("embed-refresh[%s]", sourceName)
	totalStart := time.Now()

	// Load all channels for this source.
	log.Printf("%s: loading channels for source %d ...", prefix, sourceID)
	channels, err := s.ListChannelsBySource(ctx, sourceID)
	if err != nil {
		return 0, fmt.Errorf("ListChannelsBySource: %w", err)
	}
	if len(channels) == 0 {
		log.Printf("%s: no channels found, nothing to embed", prefix)
		return 0, nil
	}
	log.Printf("%s: loaded %d channels", prefix, len(channels))

	totalBatches := (len(channels) + batchSize - 1) / batchSize
	log.Printf("%s: embedding and storing (%d/batch, %d batches) ...", prefix, batchSize, totalBatches)

	stored := 0
	for i := 0; i < len(channels); i += batchSize {
		if err := ctx.Err(); err != nil {
			return stored, fmt.Errorf("embed-refresh cancelled: %w", err)
		}

		end := i + batchSize
		if end > len(channels) {
			end = len(channels)
		}
		batch := channels[i:end]

		// Build texts and IDs for this batch only.
		batchIDs := make([]int64, len(batch))
		batchTexts := make([]string, len(batch))
		for j, ch := range batch {
			batchIDs[j] = ch.ID
			group := ""
			if ch.GroupName != nil && *ch.GroupName != "" {
				group = *ch.GroupName
			}
			batchTexts[j] = fmt.Sprintf("%s | %s | %s", ch.Name, group, mediaTypeLabel(ch.MediaType))
		}

		// Generate embeddings for this batch.
		embeddings, err := embClient.Embed(ctx, batchTexts, "document")
		if err != nil {
			return stored, fmt.Errorf("Embed batch %d: %w", (i/batchSize)+1, err)
		}

		// Store immediately — memory is freed before the next iteration.
		if err := s.StoreEmbeddings(ctx, batchIDs, embeddings); err != nil {
			return stored, fmt.Errorf("StoreEmbeddings batch %d: %w", (i/batchSize)+1, err)
		}

		stored += len(batch)
		batchNum := (i / batchSize) + 1
		if batchNum%50 == 0 || end == len(channels) {
			log.Printf("%s:   batch %d / %d  (%d channels stored)", prefix, batchNum, totalBatches, stored)
		}
	}

	log.Printf("%s: done -- %d channels embedded (%s total)", prefix, stored, formatDur(time.Since(totalStart)))
	return stored, nil
}

// mediaTypeLabel returns a human-readable label for a media type constant.
func mediaTypeLabel(mt int16) string {
	switch mt {
	case models.MediaTypeLivestream:
		return "Livestream"
	case models.MediaTypeMovie:
		return "Movie"
	case models.MediaTypeSerie:
		return "Serie"
	default:
		return "Unknown"
	}
}

// GenerateEmbeddings creates embedding text for each channel and stores the
// vectors. Embeddings are generated and stored one batch at a time to keep
// memory usage constant regardless of channel count.
func GenerateEmbeddings(ctx context.Context, s store.Store, embClient *embedding.Client, channelIDs []int64, entries []fetcher.ParsedEntry, prefix string) error {
	const batchSize = 128

	totalBatches := (len(entries) + batchSize - 1) / batchSize
	log.Printf("%s: embedding and storing (%d/batch, %d batches) ...", prefix, batchSize, totalBatches)
	start := time.Now()

	stored := 0
	for i := 0; i < len(entries); i += batchSize {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("embedding cancelled: %w", err)
		}

		end := i + batchSize
		if end > len(entries) {
			end = len(entries)
		}

		// Build texts for this batch only.
		batchIDs := channelIDs[i:end]
		batchTexts := make([]string, end-i)
		for j, e := range entries[i:end] {
			group := ""
			if e.Channel.Group != nil && *e.Channel.Group != "" {
				group = *e.Channel.Group
			}
			batchTexts[j] = fmt.Sprintf("%s | %s | %s", e.Channel.Name, group, mediaTypeLabel(e.Channel.MediaType))
		}

		// Generate embeddings for this batch.
		embeddings, err := embClient.Embed(ctx, batchTexts, "document")
		if err != nil {
			return fmt.Errorf("Embed batch %d: %w", (i/batchSize)+1, err)
		}

		// Store immediately — memory is freed before the next iteration.
		if err := s.StoreEmbeddings(ctx, batchIDs, embeddings); err != nil {
			return fmt.Errorf("StoreEmbeddings batch %d: %w", (i/batchSize)+1, err)
		}

		stored += len(batchIDs)
		batchNum := (i / batchSize) + 1
		if batchNum%50 == 0 || end == len(entries) {
			log.Printf("%s:   batch %d / %d  (%d channels stored)", prefix, batchNum, totalBatches, stored)
		}
	}

	log.Printf("%s: all embeddings stored (%d channels, %s)", prefix, stored, formatDur(time.Since(start)))
	return nil
}

// formatDur formats a duration in a human-friendly way.
func formatDur(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", m, s)
	}
}
