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
			if err := generateEmbeddings(bgCtx, s, embClient, ids, entriesCopy, prefix); err != nil {
				log.Printf("%s: warning: embedding generation failed: %v", prefix, err)
			}
		}()
		log.Printf("%s: embedding generation started in background (%d channels)", prefix, len(keepIDs))
	}
	return sourceID, channelCount, nil
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

// generateEmbeddings creates embedding text for each channel and stores the vectors.
func generateEmbeddings(ctx context.Context, s store.Store, embClient *embedding.Client, channelIDs []int64, entries []fetcher.ParsedEntry, prefix string) error {
	const batchSize = 128

	texts := make([]string, len(entries))
	for i, e := range entries {
		name := e.Channel.Name
		group := ""
		if e.Channel.Group != nil && *e.Channel.Group != "" {
			group = *e.Channel.Group
		}
		label := mediaTypeLabel(e.Channel.MediaType)
		texts[i] = fmt.Sprintf("%s | %s | %s", name, group, label)
	}

	totalBatches := (len(texts) + batchSize - 1) / batchSize
	log.Printf("%s: generating embeddings (%d/batch, %d batches) ...", prefix, batchSize, totalBatches)
	embStart := time.Now()

	onProgress := func(batchIndex, total int) {
		log.Printf("%s:   batch %d / %d embedded", prefix, batchIndex, total)
	}

	embeddings, err := embClient.EmbedBatch(ctx, texts, "document", batchSize, onProgress)
	if err != nil {
		return fmt.Errorf("EmbedBatch: %w", err)
	}

	log.Printf("%s: storing %d embeddings in database ...", prefix, len(channelIDs))
	storeStart := time.Now()

	const storeChunkSize = 10000
	for i := 0; i < len(channelIDs); i += storeChunkSize {
		end := i + storeChunkSize
		if end > len(channelIDs) {
			end = len(channelIDs)
		}

		if err := s.StoreEmbeddings(ctx, channelIDs[i:end], embeddings[i:end]); err != nil {
			return fmt.Errorf("StoreEmbeddings: %w", err)
		}

		log.Printf("%s:   %d / %d embeddings stored", prefix, end, len(channelIDs))
	}

	log.Printf("%s: all embeddings stored (%s store, %s total)", prefix, formatDur(time.Since(storeStart)), formatDur(time.Since(embStart)))
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
