package config

import "errors"

var ErrMissingDatabaseURL = errors.New("DATABASE_URL is required")
