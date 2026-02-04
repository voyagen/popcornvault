package models

import "time"

// Source represents an IPTV source (e.g. one M3U URL).
type Source struct {
	ID          int64      `json:"id,omitempty"`
	Name        string     `json:"name"`
	URL         string     `json:"url,omitempty"`
	SourceType  int16      `json:"source_type"`
	UseTvgID    *bool      `json:"use_tvg_id,omitempty"`
	UserAgent   string     `json:"user_agent,omitempty"`
	Enabled     bool       `json:"enabled"`
	LastUpdated *time.Time `json:"last_updated,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
}
