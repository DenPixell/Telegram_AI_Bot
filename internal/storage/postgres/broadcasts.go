package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"expense-bot/internal/domain"
)

const broadcastColumns = `id, text, COALESCE(created_by, 0), status, total, sent, failed, created_at, started_at, finished_at`

func scanBroadcast(row pgx.Row) (domain.Broadcast, error) {
	var b domain.Broadcast
	err := row.Scan(&b.ID, &b.Text, &b.CreatedBy, &b.Status, &b.Total, &b.Sent, &b.Failed, &b.CreatedAt, &b.StartedAt, &b.FinishedAt)
	return b, err
}

// CreateBroadcast создаёт черновик рассылки. createdBy = 0 - через HTTP API.
func (s *Store) CreateBroadcast(ctx context.Context, text string, createdBy int64) (domain.Broadcast, error) {
	b, err := scanBroadcast(s.q.QueryRow(ctx, `
		INSERT INTO broadcasts (text, created_by) VALUES ($1, NULLIF($2::bigint, 0))
		RETURNING `+broadcastColumns, text, createdBy))
	return b, mapErr("create broadcast", err)
}

func (s *Store) GetBroadcast(ctx context.Context, id int64) (domain.Broadcast, error) {
	b, err := scanBroadcast(s.q.QueryRow(ctx, `SELECT `+broadcastColumns+` FROM broadcasts WHERE id = $1`, id))
	return b, mapErr("get broadcast", err)
}

// StartBroadcast переводит черновик в отправку с числом получателей.
// ErrConflict - уже идёт другая рассылка; ErrNotFound - нет такого черновика
// (уже отправлен или отменён: повторное нажатие кнопки ничего не задвоит).
func (s *Store) StartBroadcast(ctx context.Context, id int64, total int) (domain.Broadcast, error) {
	b, err := scanBroadcast(s.q.QueryRow(ctx, `
		UPDATE broadcasts SET status = 'sending', total = $2, started_at = now()
		WHERE id = $1 AND status = 'draft'
		RETURNING `+broadcastColumns, id, total))
	if err != nil && errors.Is(mapErr("", err), domain.ErrConflict) {
		return domain.Broadcast{}, fmt.Errorf("start broadcast: another broadcast is in progress: %w", domain.ErrConflict)
	}
	return b, mapErr("start broadcast", err)
}

// UpdateBroadcastProgress сохраняет промежуточные счётчики (видны в статусе).
func (s *Store) UpdateBroadcastProgress(ctx context.Context, id int64, sent, failed int) error {
	_, err := s.q.Exec(ctx, `UPDATE broadcasts SET sent = $2, failed = $3 WHERE id = $1`, id, sent, failed)
	return mapErr("update broadcast", err)
}

func (s *Store) FinishBroadcast(ctx context.Context, id int64, sent, failed int) (domain.Broadcast, error) {
	b, err := scanBroadcast(s.q.QueryRow(ctx, `
		UPDATE broadcasts SET status = 'done', sent = $2, failed = $3, finished_at = now()
		WHERE id = $1 AND status = 'sending'
		RETURNING `+broadcastColumns, id, sent, failed))
	return b, mapErr("finish broadcast", err)
}

// CancelBroadcast отменяет черновик. Начатую рассылку отменить нельзя (ErrNotFound).
func (s *Store) CancelBroadcast(ctx context.Context, id int64) error {
	return s.execOne(ctx, "cancel broadcast",
		`UPDATE broadcasts SET status = 'cancelled', finished_at = now() WHERE id = $1 AND status = 'draft'`, id)
}

// FailStaleBroadcasts закрывает рассылки, прерванные рестартом посреди отправки,
// иначе уникальный индекс не даст начать новую.
func (s *Store) FailStaleBroadcasts(ctx context.Context) (int64, error) {
	tag, err := s.q.Exec(ctx, `UPDATE broadcasts SET status = 'done', finished_at = now() WHERE status = 'sending'`)
	if err != nil {
		return 0, mapErr("close stale broadcasts", err)
	}
	return tag.RowsAffected(), nil
}

// ActiveUserIDs - все, кто писал боту и не заблокировал его (получатели рассылки).
func (s *Store) ActiveUserIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.q.Query(ctx, `SELECT telegram_id FROM users WHERE is_active ORDER BY telegram_id`)
	if err != nil {
		return nil, mapErr("active users", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	return ids, mapErr("active users", err)
}

// CountActiveUsers - число получателей для предпросмотра.
func (s *Store) CountActiveUsers(ctx context.Context) (int, error) {
	var n int
	err := s.q.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_active`).Scan(&n)
	return n, mapErr("count active users", err)
}
