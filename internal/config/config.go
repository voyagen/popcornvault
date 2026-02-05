package config

import (
	"os"
	"time"
)

// Config holds application configuration (DB, server, and optional fetcher settings).
type Config struct {
	DatabaseURL string        `yaml:"database_url" env:"DATABASE_URL"`
	ServerPort  string        `yaml:"server_port" env:"SERVER_PORT"`
	UserAgent   string        `yaml:"user_agent" env:"FETCHER_USER_AGENT"`
	Timeout     time.Duration `yaml:"timeout" env:"FETCHER_TIMEOUT"`
}

// Load builds config from environment variables.
// If DATABASE_URL is not set, Load tries to load .env.local and .env from the current directory.
// DATABASE_URL is required. FETCHER_USER_AGENT and FETCHER_TIMEOUT are optional.
func Load() (*Config, error) {
	if os.Getenv("DATABASE_URL") == "" {
		loadEnvFiles()
	}
	c := &Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		ServerPort:  os.Getenv("SERVER_PORT"),
		UserAgent:   os.Getenv("FETCHER_USER_AGENT"),
		Timeout:     5 * time.Minute,
	}
	if c.ServerPort == "" {
		c.ServerPort = "8080"
	}
	if c.UserAgent == "" {
		c.UserAgent = "PopcornVault/1.0"
	}
	if s := os.Getenv("FETCHER_TIMEOUT"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			c.Timeout = d
		}
	}
	if c.DatabaseURL == "" {
		return nil, ErrMissingDatabaseURL
	}
	return c, nil
}
