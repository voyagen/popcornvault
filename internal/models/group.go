package models

// Group represents a category/group for channels (e.g. group-title from M3U).
type Group struct {
	ID       int64   `json:"id,omitempty"`
	Name     string  `json:"name"`
	Image    *string `json:"image,omitempty"`
	SourceID int64   `json:"source_id"`
}
