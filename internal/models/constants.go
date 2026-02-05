package models

// Source type constants (aligned with Rust source_type).
const (
	SourceTypeM3U      int16 = 0
	SourceTypeM3ULink  int16 = 1
	SourceTypeXtream   int16 = 2
	SourceTypeCustom   int16 = 3
)

// Media type constants.
const (
	MediaTypeLivestream int16 = 0
	MediaTypeMovie      int16 = 1
	MediaTypeSerie      int16 = 2
)
