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

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := server.New(pg, cfg)
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}
