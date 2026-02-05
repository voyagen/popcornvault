package models

// Channel represents a single stream entry from an M3U (name, url, group, image, media_type).
type Channel struct {
	ID        int64   `json:"id,omitempty"`
	Name      string  `json:"name"`
	URL       string  `json:"url,omitempty"`
	Group     *string `json:"group,omitempty"`
	Image     *string `json:"image,omitempty"`
	MediaType int16   `json:"media_type"`
	SourceID  int64   `json:"source_id,omitempty"`
	GroupID   *int64  `json:"group_id,omitempty"`
	Favorite  bool    `json:"favorite"`
	GroupName *string `json:"group_name,omitempty"` // populated by read queries (joined from groups table)
}
