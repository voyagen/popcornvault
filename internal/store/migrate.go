package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
)

// EnsurePgvector attempts to create the pgvector extension. If the current
// user lacks superuser privileges, it checks whether the extension already
// exists. This allows non-superuser roles to run the app as long as a DBA
// has pre-created the extension.
func EnsurePgvector(dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS vector")
	if err == nil {
		return nil // created or already existed with sufficient privileges
	}

	// If we got a permission error, check whether the extension already exists.
	if strings.Contains(err.Error(), "permission denied") {
		var exists bool
		qErr := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'vector')").Scan(&exists)
		if qErr != nil {
			return fmt.Errorf("check pgvector: %w (original: %w)", qErr, err)
		}
		if exists {
			return nil // extension was pre-created by an admin
		}
		return fmt.Errorf("pgvector extension is not installed and the current database user lacks permission to create it; "+
			"ask your database admin to run: CREATE EXTENSION vector; (original: %w)", err)
	}

	return fmt.Errorf("create pgvector extension: %w", err)
}

// RunMigrations runs SQL migrations from the given directory (e.g. "file://migrations") against the DSN.
func RunMigrations(dsn string, migrationsPath string) error {
	m, err := migrate.New(migrationsPath, dsn)
	if err != nil {
		return fmt.Errorf("migrate.New: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate.Up: %w", err)
	}
	return nil
}
