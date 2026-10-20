package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"expense-bot/internal/domain"
)

const familyColumns = `id, name, default_tz, created_at`

func scanFamily(row pgx.Row) (domain.Family, error) {
	var f domain.Family
	err := row.Scan(&f.ID, &f.Name, &f.DefaultTZ, &f.CreatedAt)
	return f, err
}

func (s *Store) CreateFamily(ctx context.Context, name, defaultTZ string) (domain.Family, error) {
	f, err := scanFamily(s.conn(ctx).QueryRow(ctx,
		`INSERT INTO families (name, default_tz) VALUES ($1, $2) RETURNING `+familyColumns,
		name, defaultTZ))
	return f, mapErr("create family", err)
}

func (s *Store) GetFamily(ctx context.Context, id int64) (domain.Family, error) {
	f, err := scanFamily(s.conn(ctx).QueryRow(ctx, `SELECT `+familyColumns+` FROM families WHERE id = $1`, id))
	return f, mapErr("get family", err)
}

// SoleFamilyID возвращает id единственной семьи (режим панели none без PANEL_FAMILY_ID).
// ErrNotFound, если семей нет, ErrConflict, если их больше одной.
func (s *Store) SoleFamilyID(ctx context.Context) (int64, error) {
	rows, err := s.conn(ctx).Query(ctx, `SELECT id FROM families ORDER BY id LIMIT 2`)
	if err != nil {
		return 0, mapErr("sole family", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, mapErr("sole family", err)
	}
	switch len(ids) {
	case 0:
		return 0, mapErr("sole family", pgx.ErrNoRows)
	case 1:
		return ids[0], nil
	default:
		return 0, mapErr("sole family: more than one family exists", domain.ErrConflict)
	}
}

func (s *Store) UpdateFamily(ctx context.Context, id int64, name, defaultTZ string) (domain.Family, error) {
	f, err := scanFamily(s.conn(ctx).QueryRow(ctx,
		`UPDATE families SET name = $2, default_tz = $3 WHERE id = $1 RETURNING `+familyColumns,
		id, name, defaultTZ))
	return f, mapErr("update family", err)
}

// DeleteFamily удаляет семью вместе с участниками, приглашениями и тратами (ON DELETE CASCADE).
func (s *Store) DeleteFamily(ctx context.Context, id int64) error {
	return s.execOne(ctx, "delete family", `DELETE FROM families WHERE id = $1`, id)
}
