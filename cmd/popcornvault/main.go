package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/voyagen/popcornvault/internal/config"
	"github.com/voyagen/popcornvault/internal/service"
	"github.com/voyagen/popcornvault/internal/store"
)

func main() {
	sourceName := flag.String("name", "", "Optional source name for the M3U URL")
	configPath := flag.String("config", "", "Optional config file path (YAML); else use env DATABASE_URL")
	flag.Parse()

	args := flag.Args()
	var m3uURL string
	if len(args) >= 1 {
		m3uURL = args[0]
	}
	if m3uURL == "" {
		fmt.Fprintln(os.Stderr, "Usage: popcornvault [flags] <m3u_url>")
		fmt.Fprintln(os.Stderr, "  -name string    optional source name")
		fmt.Fprintln(os.Stderr, "  -config string optional config file (YAML)")
		os.Exit(1)
	}

	var cfg *config.Config
	var err error
	if *configPath != "" {
		cfg, err = config.LoadFromFile(*configPath)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Run migrations from ./migrations (relative to cwd) or next to binary
	absMigrations, err := filepath.Abs("migrations")
	if err != nil {
		absMigrations = "migrations"
	}
	if _, err := os.Stat(absMigrations); err != nil {
		if exe, e := os.Executable(); e == nil {
			absMigrations = filepath.Join(filepath.Dir(exe), "migrations")
		}
	}
	migrationsPath := "file://" + absMigrations
	if err := store.RunMigrations(cfg.DatabaseURL, migrationsPath); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	pg, err := store.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db: %v\n", err)
		os.Exit(1)
	}
	defer pg.Close()

	sourceID, count, err := service.Ingest(ctx, pg, m3uURL, *sourceName, cfg.UserAgent, cfg.Timeout, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Ingested source id=%d with %d channels\n", sourceID, count)
}
