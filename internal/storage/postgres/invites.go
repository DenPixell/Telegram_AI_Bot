package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"expense-bot/internal/domain"
)

const inviteColumns = `id, family_id, code, created_by, expires_at, max_uses, used_count, revoked_at, created_at`

func scanInvite(row pgx.Row) (domain.Invite, error) {
	var i domain.Invite
	err := row.Scan(&i.ID, &i.FamilyID, &i.Code, &i.CreatedBy, &i.ExpiresAt, &i.MaxUses, &i.UsedCount, &i.RevokedAt, &i.CreatedAt)
	return i, err
}

// CreateInvite сохраняет приглашение. ErrDuplicateInviteCode - коллизия кода, нужно сгенерировать новый.
func (s *Store) CreateInvite(ctx context.Context, inv domain.Invite) (domain.Invite, error) {
	res, err := scanInvite(s.q.QueryRow(ctx, `
		INSERT INTO invites (family_id, code, created_by, expires_at, max_uses)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+inviteColumns,
		inv.FamilyID, inv.Code, inv.CreatedBy, inv.ExpiresAt, inv.MaxUses))
	return res, mapErr("create invite", err)
}

// UseInvite атомарно засчитывает использование кода и возвращает приглашение.
// Неверный, истёкший, отозванный и исчерпанный код дают одну ошибку ErrInviteInvalid.
// Вызывать в одной транзакции с AddMember, чтобы откат вернул использование.
func (s *Store) UseInvite(ctx context.Context, code string) (domain.Invite, error) {
	inv, err := scanInvite(s.q.QueryRow(ctx, `
		UPDATE invites SET used_count = used_count + 1
		WHERE code = $1 AND revoked_at IS NULL AND expires_at > now() AND used_count < max_uses
		RETURNING `+inviteColumns, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invite{}, fmt.Errorf("use invite: %w", domain.ErrInviteInvalid)
	}
	return inv, mapErr("use invite", err)
}

// ListActiveInvites возвращает действующие приглашения семьи, новые первыми.
func (s *Store) ListActiveInvites(ctx context.Context, familyID int64) ([]domain.Invite, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+inviteColumns+` FROM invites
		WHERE family_id = $1 AND revoked_at IS NULL AND expires_at > now() AND used_count < max_uses
		ORDER BY created_at DESC, id DESC`, familyID)
	if err != nil {
		return nil, mapErr("list invites", err)
	}
	res, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Invite, error) { return scanInvite(row) })
	return res, mapErr("list invites", err)
}

// RevokeInvite отзывает приглашение семьи. Повторный отзыв даёт ErrNotFound.
func (s *Store) RevokeInvite(ctx context.Context, familyID, inviteID int64) error {
	return s.execOne(ctx, "revoke invite",
		`UPDATE invites SET revoked_at = now() WHERE family_id = $1 AND id = $2 AND revoked_at IS NULL`,
		familyID, inviteID)
}
