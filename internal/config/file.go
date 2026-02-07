package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type fileConfig struct {
	DatabaseURL  string `yaml:"database_url"`
	ServerPort   string `yaml:"server_port"`
	UserAgent    string `yaml:"user_agent"`
	Timeout      string `yaml:"timeout"`
	VoyageAPIKey string `yaml:"voyage_api_key"`
	VoyageModel  string `yaml:"voyage_model"`
}

// LoadFromFile loads config from a YAML file. database_url is required.
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f fileConfig
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.DatabaseURL == "" {
		return nil, ErrMissingDatabaseURL
	}
	c := &Config{
		DatabaseURL:  f.DatabaseURL,
		ServerPort:   f.ServerPort,
		UserAgent:    f.UserAgent,
		Timeout:      30 * time.Second,
		VoyageAPIKey: f.VoyageAPIKey,
		VoyageModel:  f.VoyageModel,
	}
	if c.ServerPort == "" {
		c.ServerPort = "8080"
	}
	if c.UserAgent == "" {
		c.UserAgent = "PopcornVault/1.0"
	}
	if f.Timeout != "" {
		if d, err := time.ParseDuration(f.Timeout); err == nil {
			c.Timeout = d
		}
	}
	return c, nil
}
