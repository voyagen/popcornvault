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
	"github.com/voyagen/popcornvault/internal/server"
	"github.com/voyagen/popcornvault/internal/service"
	"github.com/voyagen/popcornvault/internal/store"
)

func main() {
	serve := flag.Bool("serve", false, "Run as a long-lived HTTP server")
	sourceName := flag.String("name", "", "Optional source name for the M3U URL")
	configPath := flag.String("config", "", "Optional config file path (YAML); else use env DATABASE_URL")
	flag.Parse()

	// In serve mode we don't require an M3U URL argument.
	args := flag.Args()
	var m3uURL string
	if len(args) >= 1 {
		m3uURL = args[0]
	}
	if !*serve && m3uURL == "" {
		fmt.Fprintln(os.Stderr, "Usage: popcornvault [flags] <m3u_url>")
		fmt.Fprintln(os.Stderr, "       popcornvault -serve")
		fmt.Fprintln(os.Stderr, "  -serve         run as HTTP server")
		fmt.Fprintln(os.Stderr, "  -name string   optional source name")
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

	if *serve {
		runServer(ctx, pg, cfg)
	} else {
		runIngest(ctx, pg, cfg, m3uURL, *sourceName)
	}
}

func runServer(ctx context.Context, pg *store.Postgres, cfg *config.Config) {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := server.New(pg, cfg)
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}

func runIngest(ctx context.Context, pg *store.Postgres, cfg *config.Config, m3uURL, sourceName string) {
	sourceID, count, err := service.Ingest(ctx, pg, m3uURL, sourceName, cfg.UserAgent, cfg.Timeout, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Ingested source id=%d with %d channels\n", sourceID, count)
}
