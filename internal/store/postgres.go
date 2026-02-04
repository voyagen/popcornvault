package store

import (
	"context"
	"fmt"

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
