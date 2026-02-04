package models

// ChannelHttpHeaders holds optional HTTP headers for a channel (from EXTVLCOPT).
type ChannelHttpHeaders struct {
	ID        int64   `json:"id,omitempty"`
	ChannelID int64   `json:"channel_id,omitempty"`
	Referrer  *string `json:"referrer,omitempty"`
	UserAgent *string `json:"user_agent,omitempty"`
	HTTPOrigin *string `json:"http_origin,omitempty"`
	IgnoreSSL *bool  `json:"ignore_ssl,omitempty"`
}
