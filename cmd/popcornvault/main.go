package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/voyagen/popcornvault/internal/cache"
	"github.com/voyagen/popcornvault/internal/config"
	"github.com/voyagen/popcornvault/internal/embedding"
	"github.com/voyagen/popcornvault/internal/server"
	"github.com/voyagen/popcornvault/internal/service"
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
		embedder = embedding.NewClient(cfg.VoyageAPIKey)
		fmt.Fprintln(os.Stderr, "semantic search enabled (VoyageAI)")
	} else {
		fmt.Fprintln(os.Stderr, "semantic search disabled (VOYAGE_API_KEY not set)")
	}

	// Connect to Redis if REDIS_URL is configured.
	var rds *cache.Redis
	var appStore store.Store = pg
	if cfg.RedisURL != "" {
		rds, err = cache.New(cfg.RedisURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "redis: %v\n", err)
			os.Exit(1)
		}
		defer rds.Close()

		if err := rds.Ping(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "redis ping: %v\n", err)
			os.Exit(1)
		}

		appStore = store.NewCachedStore(pg, rds)
		fmt.Fprintln(os.Stderr, "redis connected (caching enabled)")
	} else {
		fmt.Fprintln(os.Stderr, "redis disabled (REDIS_URL not set)")
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start the background embedding job worker if both Redis and embedder are available.
	if rds != nil && embedder != nil {
		go runEmbeddingWorker(ctx, rds, appStore, embedder)
	}

	srv := server.New(appStore, cfg, embedder, rds)
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}

// runEmbeddingWorker continuously dequeues embedding jobs from Redis and
// processes them. It stops when ctx is cancelled (graceful shutdown).
func runEmbeddingWorker(ctx context.Context, rds *cache.Redis, s store.Store, embedder *embedding.Client) {
	log.Println("embedding worker started")
	for {
		select {
		case <-ctx.Done():
			log.Println("embedding worker stopping")
			return
		default:
		}

		job, err := cache.Dequeue(ctx, rds, cache.DefaultQueue, 5*time.Second)
		if err != nil {
			log.Printf("embedding worker: dequeue error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if job == nil {
			continue // timeout, loop back to check ctx
		}

		log.Printf("embedding worker: processing job source_id=%d source=%q embeddings_only=%v",
			job.SourceID, job.SourceName, job.EmbeddingsOnly)

		if job.EmbeddingsOnly {
			if _, err := service.RefreshEmbeddings(ctx, s, embedder, job.SourceID, job.SourceName); err != nil {
				log.Printf("embedding worker: RefreshEmbeddings error: %v", err)
			}
		}
	}
}
