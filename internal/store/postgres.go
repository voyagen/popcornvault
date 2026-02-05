package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

// WipeSourceChannels deletes all channels (and their headers) for the source, and groups for the source.
func (p *Postgres) WipeSourceChannels(ctx context.Context, sourceID int64) error {
	// Delete channel_http_headers for channels of this source, then channels, then groups.
	_, err := p.pool.Exec(ctx,
		`DELETE FROM channel_http_headers WHERE channel_id IN (SELECT id FROM channels WHERE source_id = $1)`,
		sourceID,
	)
	if err != nil {
		return fmt.Errorf("delete channel_http_headers: %w", err)
	}
	_, err = p.pool.Exec(ctx, `DELETE FROM channels WHERE source_id = $1`, sourceID)
	if err != nil {
		return fmt.Errorf("delete channels: %w", err)
	}
	_, err = p.pool.Exec(ctx, `DELETE FROM groups WHERE source_id = $1`, sourceID)
	if err != nil {
		return fmt.Errorf("delete groups: %w", err)
	}
	return nil
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
		return fmt.Errorf("UpdateSource: source %d not found", sourceID)
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
		return fmt.Errorf("DeleteSource: source %d not found", sourceID)
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
		return fmt.Errorf("ToggleChannelFavorite: channel %d not found", channelID)
	}
	return nil
}
