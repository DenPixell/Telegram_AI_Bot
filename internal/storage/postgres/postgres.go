// Package postgres - хранилище на PostgreSQL. Все запросы к пользовательским данным содержат
// family_id и, где нужно, user_id.
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier - общее у пула и транзакции, чтобы одни и те же методы работали в обоих режимах.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Ping проверяет доступность БД (для /readyz).
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// txKey - ключ открытой транзакции в context.
type txKey struct{}

// conn возвращает транзакцию из ctx, если она открыта через WithTx, иначе пул.
// Так одни и те же методы работают и внутри транзакции, и вне её, а вызывающему
// (сервису) не нужно знать о pgx.
func (s *Store) conn(ctx context.Context) querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return s.pool
}

// WithTx выполняет fn в транзакции: commit при nil, rollback при ошибке или панике.
// Все методы Store, вызванные с переданным в fn контекстом, идут в эту транзакцию.
// Внутри уже открытой транзакции создаётся savepoint.
func (s *Store) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return pgx.BeginFunc(ctx, s.conn(ctx), func(tx pgx.Tx) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// LockUser сериализует операции одного пользователя до конца текущей транзакции
// («удали последнее» и «измени последнее» подряд). Вне транзакции бессмысленен.
func (s *Store) LockUser(ctx context.Context, userID int64) error {
	q, ok := s.conn(ctx).(pgx.Tx)
	if !ok {
		return fmt.Errorf("lock user: must be called inside WithTx")
	}
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	return nil
}

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
