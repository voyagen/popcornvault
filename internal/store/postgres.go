package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
	"github.com/voyagen/popcornvault/internal/models"
)

// Postgres implements Store using PostgreSQL.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres creates a Postgres store from a DSN. Caller must call Close when done.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// Close closes the connection pool.
func (p *Postgres) Close() {
	p.pool.Close()
}

// CreateOrGetSource creates a source by name if not exists, returns id.
func (p *Postgres) CreateOrGetSource(ctx context.Context, name, url string, sourceType int16, userAgent string) (int64, error) {
	var id int64
	err := p.pool.QueryRow(ctx,
		`INSERT INTO sources (name, source_type, url, user_agent, enabled)
		 VALUES ($1, $2, $3, NULLIF($4,''), true)
		 ON CONFLICT (name) DO UPDATE SET url = EXCLUDED.url, user_agent = EXCLUDED.user_agent
		 RETURNING id`,
		name, sourceType, url, userAgent,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("CreateOrGetSource: %w", err)
	}
	return id, nil
}

// RemoveStaleChannels deletes channels (and their headers via CASCADE) for the
// source whose IDs are NOT in keepIDs. This is used during refresh to prune
// channels that no longer exist in the upstream M3U without touching favourites
// or other user data on channels that still exist.
// Returns the number of deleted channels.
//
// For large channel counts, uses a temporary table instead of an array parameter
// to avoid PostgreSQL performance issues with huge ANY/ALL arrays.
func (p *Postgres) RemoveStaleChannels(ctx context.Context, sourceID int64, keepIDs []int64) (int64, error) {
	if len(keepIDs) == 0 {
		// Nothing to keep — delete every channel for this source.
		tag, err := p.pool.Exec(ctx,
			`DELETE FROM channels WHERE source_id = $1`, sourceID)
		if err != nil {
			return 0, fmt.Errorf("RemoveStaleChannels (all): %w", err)
		}
		return tag.RowsAffected(), nil
	}

	// Use a transaction with a temp table for efficient bulk exclusion.
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("RemoveStaleChannels begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Drop any leftover temp table from a previous session on this connection,
	// then create a fresh one without constraints for fast COPY inserts.
	if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS _keep_ids`); err != nil {
		return 0, fmt.Errorf("RemoveStaleChannels drop temp: %w", err)
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE _keep_ids (id BIGINT) ON COMMIT DROP`); err != nil {
		return 0, fmt.Errorf("RemoveStaleChannels create temp: %w", err)
	}

	// Bulk-insert keepIDs using COPY for speed.
	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"_keep_ids"},
		[]string{"id"},
		&int64CopySource{ids: keepIDs},
	)
	if err != nil {
		return 0, fmt.Errorf("RemoveStaleChannels copy: %w", err)
	}

	// Index after bulk insert is faster than maintaining an index during COPY.
	if _, err := tx.Exec(ctx, `CREATE INDEX ON _keep_ids (id)`); err != nil {
		return 0, fmt.Errorf("RemoveStaleChannels index temp: %w", err)
	}

	// Analyze the temp table so the planner picks a good join strategy.
	if _, err := tx.Exec(ctx, `ANALYZE _keep_ids`); err != nil {
		return 0, fmt.Errorf("RemoveStaleChannels analyze temp: %w", err)
	}

	// Delete channels not in the keep set.
	tag, err := tx.Exec(ctx,
		`DELETE FROM channels c
		 WHERE c.source_id = $1
		   AND NOT EXISTS (SELECT 1 FROM _keep_ids k WHERE k.id = c.id)`,
		sourceID)
	if err != nil {
		return 0, fmt.Errorf("RemoveStaleChannels delete: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("RemoveStaleChannels commit: %w", err)
	}

	return tag.RowsAffected(), nil
}

// int64CopySource implements pgx.CopyFromSource for a slice of int64 values.
type int64CopySource struct {
	ids []int64
	idx int
}

func (s *int64CopySource) Next() bool {
	return s.idx < len(s.ids)
}

func (s *int64CopySource) Values() ([]any, error) {
	v := s.ids[s.idx]
	s.idx++
	return []any{v}, nil
}

func (s *int64CopySource) Err() error {
	return nil
}

// RemoveOrphanedGroups deletes groups for the source that have no remaining channels.
// Returns the number of deleted groups.
func (p *Postgres) RemoveOrphanedGroups(ctx context.Context, sourceID int64) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM groups
		 WHERE source_id = $1
		   AND id NOT IN (SELECT DISTINCT group_id FROM channels WHERE source_id = $1 AND group_id IS NOT NULL)`,
		sourceID)
	if err != nil {
		return 0, fmt.Errorf("RemoveOrphanedGroups: %w", err)
	}
	return tag.RowsAffected(), nil
}

// GetOrCreateGroup returns group id for name/sourceID.
func (p *Postgres) GetOrCreateGroup(ctx context.Context, sourceID int64, name string, image *string) (int64, error) {
	var id int64
	err := p.pool.QueryRow(ctx,
		`INSERT INTO groups (name, image, source_id) VALUES ($1, $2, $3)
		 ON CONFLICT (name, source_id) DO UPDATE SET image = COALESCE(EXCLUDED.image, groups.image)
		 RETURNING id`,
		name, image, sourceID,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("GetOrCreateGroup: %w", err)
	}
	return id, nil
}

// UpsertChannel inserts or updates a channel; returns channel id.
func (p *Postgres) UpsertChannel(ctx context.Context, ch *models.Channel) (int64, error) {
	var id int64
	err := p.pool.QueryRow(ctx,
		`INSERT INTO channels (name, image, url, media_type, source_id, group_id, favorite)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (name, source_id, url) DO UPDATE SET
		   image = EXCLUDED.image, media_type = EXCLUDED.media_type, group_id = EXCLUDED.group_id
		 RETURNING id`,
		ch.Name, ch.Image, ch.URL, ch.MediaType, ch.SourceID, ch.GroupID, ch.Favorite,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("UpsertChannel: %w", err)
	}
	return id, nil
}

// UpsertChannelHeaders inserts or updates headers for a channel.
func (p *Postgres) UpsertChannelHeaders(ctx context.Context, channelID int64, h *models.ChannelHttpHeaders) error {
	ignoreSSL := false
	if h.IgnoreSSL != nil {
		ignoreSSL = *h.IgnoreSSL
	}
	_, err := p.pool.Exec(ctx,
		`INSERT INTO channel_http_headers (channel_id, referrer, user_agent, http_origin, ignore_ssl)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (channel_id) DO UPDATE SET
		   referrer = EXCLUDED.referrer, user_agent = EXCLUDED.user_agent,
		   http_origin = EXCLUDED.http_origin, ignore_ssl = EXCLUDED.ignore_ssl`,
		channelID, h.Referrer, h.UserAgent, h.HTTPOrigin, ignoreSSL,
	)
	if err != nil {
		return fmt.Errorf("UpsertChannelHeaders: %w", err)
	}
	return nil
}

// UpdateSourceLastUpdated sets last_updated for the source.
func (p *Postgres) UpdateSourceLastUpdated(ctx context.Context, sourceID int64) error {
	_, err := p.pool.Exec(ctx, `UPDATE sources SET last_updated = NOW() WHERE id = $1`, sourceID)
	if err != nil {
		return fmt.Errorf("UpdateSourceLastUpdated: %w", err)
	}
	return nil
}

// ListSources returns all sources ordered by id.
func (p *Postgres) ListSources(ctx context.Context) ([]models.Source, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT id, name, source_type, url, use_tvg_id, user_agent, enabled, last_updated, created_at
		 FROM sources ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("ListSources: %w", err)
	}
	defer rows.Close()

	var sources []models.Source
	for rows.Next() {
		var s models.Source
		var userAgent *string
		if err := rows.Scan(&s.ID, &s.Name, &s.SourceType, &s.URL, &s.UseTvgID, &userAgent, &s.Enabled, &s.LastUpdated, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("ListSources scan: %w", err)
		}
		if userAgent != nil {
			s.UserAgent = *userAgent
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// GetChannelByID returns a single channel by id with group name joined.
func (p *Postgres) GetChannelByID(ctx context.Context, channelID int64) (*models.Channel, error) {
	var ch models.Channel
	err := p.pool.QueryRow(ctx,
		`SELECT c.id, c.name, c.image, c.url, c.media_type, c.source_id, c.group_id, c.favorite, g.name
		 FROM channels c
		 LEFT JOIN groups g ON c.group_id = g.id
		 WHERE c.id = $1`, channelID,
	).Scan(&ch.ID, &ch.Name, &ch.Image, &ch.URL, &ch.MediaType, &ch.SourceID, &ch.GroupID, &ch.Favorite, &ch.GroupName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("channel %d: %w", channelID, ErrNotFound)
		}
		return nil, fmt.Errorf("GetChannelByID: %w", err)
	}
	return &ch, nil
}

// ListChannels returns channels matching the filter and total count (before limit/offset).
func (p *Postgres) ListChannels(ctx context.Context, filter ChannelFilter) ([]models.Channel, int, error) {
	// Apply defaults.
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}

	// Build dynamic WHERE clause.
	where := []string{}
	args := []any{}
	argIdx := 1

	if filter.SourceID != nil {
		where = append(where, fmt.Sprintf("c.source_id = $%d", argIdx))
		args = append(args, *filter.SourceID)
		argIdx++
	}
	if filter.GroupID != nil {
		where = append(where, fmt.Sprintf("c.group_id = $%d", argIdx))
		args = append(args, *filter.GroupID)
		argIdx++
	}
	if filter.MediaType != nil {
		where = append(where, fmt.Sprintf("c.media_type = $%d", argIdx))
		args = append(args, *filter.MediaType)
		argIdx++
	}
	if filter.Favorite != nil {
		where = append(where, fmt.Sprintf("c.favorite = $%d", argIdx))
		args = append(args, *filter.Favorite)
		argIdx++
	}
	if filter.Search != "" {
		where = append(where, fmt.Sprintf("c.name ILIKE $%d", argIdx))
		args = append(args, "%"+filter.Search+"%")
		argIdx++
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	// Count query.
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM channels c %s`, whereClause)
	var total int
	if err := p.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("ListChannels count: %w", err)
	}

	// Data query with LEFT JOIN on groups for group_name.
	dataQuery := fmt.Sprintf(
		`SELECT c.id, c.name, c.image, c.url, c.media_type, c.source_id, c.group_id, c.favorite, g.name
		 FROM channels c
		 LEFT JOIN groups g ON c.group_id = g.id
		 %s
		 ORDER BY c.name
		 LIMIT $%d OFFSET $%d`,
		whereClause, argIdx, argIdx+1,
	)
	dataArgs := append(args, filter.Limit, filter.Offset)

	rows, err := p.pool.Query(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("ListChannels query: %w", err)
	}
	defer rows.Close()

	var channels []models.Channel
	for rows.Next() {
		var ch models.Channel
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Image, &ch.URL, &ch.MediaType, &ch.SourceID, &ch.GroupID, &ch.Favorite, &ch.GroupName); err != nil {
			return nil, 0, fmt.Errorf("ListChannels scan: %w", err)
		}
		channels = append(channels, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("ListChannels rows: %w", err)
	}
	return channels, total, nil
}

// ListGroups returns groups, optionally filtered by source id, ordered by name.
func (p *Postgres) ListGroups(ctx context.Context, sourceID *int64) ([]models.Group, error) {
	var rows pgx.Rows
	var err error
	if sourceID != nil {
		rows, err = p.pool.Query(ctx,
			`SELECT id, name, image, source_id FROM groups WHERE source_id = $1 ORDER BY name`,
			*sourceID,
		)
	} else {
		rows, err = p.pool.Query(ctx,
			`SELECT id, name, image, source_id FROM groups ORDER BY name`)
	}
	if err != nil {
		return nil, fmt.Errorf("ListGroups: %w", err)
	}
	defer rows.Close()

	var groups []models.Group
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Image, &g.SourceID); err != nil {
			return nil, fmt.Errorf("ListGroups scan: %w", err)
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// GetSourceByID returns a single source by id.
func (p *Postgres) GetSourceByID(ctx context.Context, sourceID int64) (*models.Source, error) {
	var s models.Source
	var userAgent *string
	err := p.pool.QueryRow(ctx,
		`SELECT id, name, source_type, url, use_tvg_id, user_agent, enabled, last_updated, created_at
		 FROM sources WHERE id = $1`, sourceID,
	).Scan(&s.ID, &s.Name, &s.SourceType, &s.URL, &s.UseTvgID, &userAgent, &s.Enabled, &s.LastUpdated, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("source %d: %w", sourceID, ErrNotFound)
		}
		return nil, fmt.Errorf("GetSourceByID: %w", err)
	}
	if userAgent != nil {
		s.UserAgent = *userAgent
	}
	return &s, nil
}

// UpdateSource updates mutable fields of a source. Only non-nil fields in SourceUpdate are applied.
func (p *Postgres) UpdateSource(ctx context.Context, sourceID int64, fields SourceUpdate) error {
	setClauses := []string{}
	args := []any{}
	idx := 1

	if fields.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", idx))
		args = append(args, *fields.Name)
		idx++
	}
	if fields.URL != nil {
		setClauses = append(setClauses, fmt.Sprintf("url = $%d", idx))
		args = append(args, *fields.URL)
		idx++
	}
	if fields.UserAgent != nil {
		setClauses = append(setClauses, fmt.Sprintf("user_agent = $%d", idx))
		args = append(args, *fields.UserAgent)
		idx++
	}
	if fields.Enabled != nil {
		setClauses = append(setClauses, fmt.Sprintf("enabled = $%d", idx))
		args = append(args, *fields.Enabled)
		idx++
	}

	if len(setClauses) == 0 {
		return nil // nothing to update
	}

	query := fmt.Sprintf("UPDATE sources SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "), idx)
	args = append(args, sourceID)

	tag, err := p.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("UpdateSource: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("source %d: %w", sourceID, ErrNotFound)
	}
	return nil
}

// DeleteSource deletes a source by id. Related channels and groups are removed via ON DELETE CASCADE.
func (p *Postgres) DeleteSource(ctx context.Context, sourceID int64) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM sources WHERE id = $1", sourceID)
	if err != nil {
		return fmt.Errorf("DeleteSource: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("source %d: %w", sourceID, ErrNotFound)
	}
	return nil
}

// ToggleChannelFavorite sets the favorite flag on a channel.
func (p *Postgres) ToggleChannelFavorite(ctx context.Context, channelID int64, favorite bool) error {
	tag, err := p.pool.Exec(ctx, "UPDATE channels SET favorite = $1 WHERE id = $2", favorite, channelID)
	if err != nil {
		return fmt.Errorf("ToggleChannelFavorite: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("channel %d: %w", channelID, ErrNotFound)
	}
	return nil
}

// CountChannelsBySource returns the total number of channels for a source.
func (p *Postgres) CountChannelsBySource(ctx context.Context, sourceID int64) (int64, error) {
	var count int64
	err := p.pool.QueryRow(ctx, `SELECT COUNT(*) FROM channels WHERE source_id = $1`, sourceID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("CountChannelsBySource: %w", err)
	}
	return count, nil
}

// StoreEmbeddings batch-updates the embedding column for the given channel IDs.
// Sends updates in chunks of 5,000 to avoid overwhelming PostgreSQL.
func (p *Postgres) StoreEmbeddings(ctx context.Context, channelIDs []int64, embeddings [][]float32) error {
	if len(channelIDs) != len(embeddings) {
		return fmt.Errorf("StoreEmbeddings: channelIDs length (%d) != embeddings length (%d)", len(channelIDs), len(embeddings))
	}

	const chunkSize = 5000
	total := len(channelIDs)

	for start := 0; start < total; start += chunkSize {
		end := start + chunkSize
		if end > total {
			end = total
		}

		batch := &pgx.Batch{}
		for i := start; i < end; i++ {
			vec := pgvector.NewVector(embeddings[i])
			batch.Queue("UPDATE channels SET embedding = $1 WHERE id = $2", vec, channelIDs[i])
		}

		br := p.pool.SendBatch(ctx, batch)
		for i := start; i < end; i++ {
			if _, err := br.Exec(); err != nil {
				br.Close()
				return fmt.Errorf("StoreEmbeddings id=%d: %w", channelIDs[i], err)
			}
		}
		br.Close()
	}
	return nil
}

// SemanticSearch returns channels ordered by cosine similarity to queryVec.
func (p *Postgres) SemanticSearch(ctx context.Context, queryVec []float32, filter ChannelFilter) ([]SemanticResult, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}

	vec := pgvector.NewVector(queryVec)

	where := []string{"c.embedding IS NOT NULL"}
	args := []any{vec}
	argIdx := 2 // $1 is the query vector

	if filter.SourceID != nil {
		where = append(where, fmt.Sprintf("c.source_id = $%d", argIdx))
		args = append(args, *filter.SourceID)
		argIdx++
	}
	if filter.GroupID != nil {
		where = append(where, fmt.Sprintf("c.group_id = $%d", argIdx))
		args = append(args, *filter.GroupID)
		argIdx++
	}
	if filter.MediaType != nil {
		where = append(where, fmt.Sprintf("c.media_type = $%d", argIdx))
		args = append(args, *filter.MediaType)
		argIdx++
	}
	if filter.Favorite != nil {
		where = append(where, fmt.Sprintf("c.favorite = $%d", argIdx))
		args = append(args, *filter.Favorite)
		argIdx++
	}

	whereClause := "WHERE " + strings.Join(where, " AND ")

	query := fmt.Sprintf(
		`SELECT c.id, c.name, c.image, c.url, c.media_type, c.source_id, c.group_id, c.favorite, g.name,
		        1 - (c.embedding <=> $1) AS similarity
		 FROM channels c
		 LEFT JOIN groups g ON c.group_id = g.id
		 %s
		 ORDER BY c.embedding <=> $1
		 LIMIT $%d`,
		whereClause, argIdx,
	)
	args = append(args, filter.Limit)

	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("SemanticSearch: %w", err)
	}
	defer rows.Close()

	var results []SemanticResult
	for rows.Next() {
		var r SemanticResult
		if err := rows.Scan(
			&r.Channel.ID, &r.Channel.Name, &r.Channel.Image, &r.Channel.URL,
			&r.Channel.MediaType, &r.Channel.SourceID, &r.Channel.GroupID,
			&r.Channel.Favorite, &r.Channel.GroupName, &r.Similarity,
		); err != nil {
			return nil, fmt.Errorf("SemanticSearch scan: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("SemanticSearch rows: %w", err)
	}
	return results, nil
}

// ListChannelsBySource returns all channels for a source (with group name joined).
func (p *Postgres) ListChannelsBySource(ctx context.Context, sourceID int64) ([]models.Channel, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT c.id, c.name, c.image, c.url, c.media_type, c.source_id, c.group_id, c.favorite, g.name
		 FROM channels c
		 LEFT JOIN groups g ON c.group_id = g.id
		 WHERE c.source_id = $1
		 ORDER BY c.id`,
		sourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("ListChannelsBySource: %w", err)
	}
	defer rows.Close()

	var channels []models.Channel
	for rows.Next() {
		var ch models.Channel
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Image, &ch.URL, &ch.MediaType, &ch.SourceID, &ch.GroupID, &ch.Favorite, &ch.GroupName); err != nil {
			return nil, fmt.Errorf("ListChannelsBySource scan: %w", err)
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// ListChannelsWithoutEmbeddings returns channels for a source that have no embedding yet.
func (p *Postgres) ListChannelsWithoutEmbeddings(ctx context.Context, sourceID int64, limit int) ([]models.Channel, error) {
	if limit <= 0 {
		limit = 1000
	}

	rows, err := p.pool.Query(ctx,
		`SELECT c.id, c.name, c.image, c.url, c.media_type, c.source_id, c.group_id, c.favorite, g.name
		 FROM channels c
		 LEFT JOIN groups g ON c.group_id = g.id
		 WHERE c.source_id = $1 AND c.embedding IS NULL
		 ORDER BY c.id
		 LIMIT $2`,
		sourceID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("ListChannelsWithoutEmbeddings: %w", err)
	}
	defer rows.Close()

	var channels []models.Channel
	for rows.Next() {
		var ch models.Channel
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Image, &ch.URL, &ch.MediaType, &ch.SourceID, &ch.GroupID, &ch.Favorite, &ch.GroupName); err != nil {
			return nil, fmt.Errorf("ListChannelsWithoutEmbeddings scan: %w", err)
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}
