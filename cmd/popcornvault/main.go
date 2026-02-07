package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/voyagen/popcornvault/internal/config"
	"github.com/voyagen/popcornvault/internal/embedding"
	"github.com/voyagen/popcornvault/internal/server"
	"github.com/voyagen/popcornvault/internal/store"
)

func main() {
	configPath := flag.String("config", "", "Optional config file path (YAML); else use env DATABASE_URL")
	flag.Parse()

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

	// Run migrations.
	absMigrations, err := filepath.Abs("migrations")
	if err != nil {
		absMigrations = "migrations"
	}
	if _, err := os.Stat(absMigrations); err != nil {
		if exe, e := os.Executable(); e == nil {
			absMigrations = filepath.Join(filepath.Dir(exe), "migrations")
		}
	}
	// Ensure pgvector extension exists before running migrations.
	if err := store.EnsurePgvector(cfg.DatabaseURL); err != nil {
		fmt.Fprintf(os.Stderr, "pgvector: %v\n", err)
		os.Exit(1)
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

	// Create embedding client if VOYAGE_API_KEY is configured.
	var embedder *embedding.Client
	if cfg.VoyageAPIKey != "" {
		embedder = embedding.NewClient(cfg.VoyageAPIKey, cfg.VoyageModel)
		fmt.Fprintln(os.Stderr, "semantic search enabled (VoyageAI)")
	} else {
		fmt.Fprintln(os.Stderr, "semantic search disabled (VOYAGE_API_KEY not set)")
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := server.New(pg, cfg, embedder)
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}
