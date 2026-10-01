package db

import (
	"context"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect returns nil pool when DATABASE_URL is empty (memory mode).
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	if databaseURL == "" {
		return nil, nil
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 5 // Neon free-tier friendly
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// RunMigrations executes migrations/001_init.sql (and any future files).
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return nil
	}
	sql, err := os.ReadFile("migrations/001_init.sql")
	if err != nil {
		// also try ./migrations relative to binary workdir variants
		return err
	}
	_, err = pool.Exec(ctx, string(sql))
	return err
}
