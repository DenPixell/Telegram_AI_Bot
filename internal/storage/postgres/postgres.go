// Package postgres - хранилище на PostgreSQL. Все запросы содержат family_id и, где нужно, user_id.
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Ping проверяет доступность БД (для /readyz).
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Connect создаёт пул и ждёт готовности БД, повторяя попытки до отмены ctx.
// statement_timeout ограничивает любые тяжёлые запросы.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DB_DSN: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	backoff := 500 * time.Millisecond
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		slog.WarnContext(ctx, "database not ready, retrying", "err", err, "in", backoff.String())
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("connect to database: %w", err)
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}
