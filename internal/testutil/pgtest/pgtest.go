// Package pgtest - PostgreSQL для интеграционных тестов.
//
// Каждый тестовый бинарник (пакет) получает свою базу:
//   - если задан TEST_DB_DSN - создаётся временная БД на этом сервере и удаляется в Main;
//   - иначе поднимается контейнер postgres:16-alpine через testcontainers (его убирает Ryuk).
//
// Без Docker и без TEST_DB_DSN интеграционные тесты пропускаются, а не падают.
// go test -short тоже пропускает их.
//
// Использование:
//
//	func TestMain(m *testing.M) { pgtest.Main(m) }
//
//	func TestX(t *testing.T) {
//		store, pool := pgtest.New(t) // миграции применены, таблицы пусты
//	}
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"expense-bot/internal/storage/postgres"
)

var (
	once      sync.Once
	dsn       string
	setupErr  error
	adminDSN  string // для удаления временной БД при TEST_DB_DSN
	tempDB    string
	migrateMu sync.Mutex
	migrated  bool
)

// Main запускает тесты пакета и удаляет временную БД (если она создавалась на TEST_DB_DSN).
func Main(m *testing.M) {
	code := m.Run()
	if tempDB != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if conn, err := pgx.Connect(ctx, adminDSN); err == nil {
			_, _ = conn.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{tempDB}.Sanitize()+` WITH (FORCE)`)
			_ = conn.Close(ctx)
		}
		cancel()
	}
	os.Exit(code)
}

// New возвращает Store и пул на чистой базе: миграции применены, все таблицы пусты.
func New(t testing.TB) (*postgres.Store, *pgxpool.Pool) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: skipped with -short")
	}
	once.Do(setup)
	if setupErr != nil {
		t.Skipf("postgres unavailable (set TEST_DB_DSN or start Docker): %v", setupErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	migrateMu.Lock()
	if !migrated {
		if err := postgres.Migrate(ctx, pool); err != nil {
			migrateMu.Unlock()
			t.Fatalf("migrate: %v", err)
		}
		migrated = true
	}
	migrateMu.Unlock()
	Truncate(t, pool)
	return postgres.New(pool), pool
}

// Truncate очищает все таблицы приложения (кроме служебной таблицы goose).
func Truncate(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	if len(names) == 0 {
		return
	}
	for i, n := range names {
		names[i] = pgx.Identifier{n}.Sanitize()
	}
	if _, err := pool.Exec(ctx, `TRUNCATE `+strings.Join(names, ", ")+` RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func setup() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if base := os.Getenv("TEST_DB_DSN"); base != "" {
		dsn, setupErr = tempDatabase(ctx, base)
		return
	}
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("expenses"), tcpostgres.WithUsername("app"), tcpostgres.WithPassword("app"),
		tcpostgres.BasicWaitStrategies())
	if err != nil {
		setupErr = err
		return
	}
	dsn, setupErr = c.ConnectionString(ctx, "sslmode=disable")
}

// tempDatabase создаёт на сервере из base отдельную БД для этого тестового бинарника.
func tempDatabase(ctx context.Context, base string) (string, error) {
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close(ctx) }()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	tempDB = "expbot_test_" + hex.EncodeToString(b)
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{tempDB}.Sanitize()); err != nil {
		tempDB = ""
		return "", fmt.Errorf("create test database: %w", err)
	}
	adminDSN = base
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/" + tempDB
	return u.String(), nil
}
