package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"expense-bot/internal/domain"
)

const memberColumns = `m.user_id, m.family_id, m.role, m.relation, m.joined_at`

func scanMember(row pgx.Row) (domain.Member, error) {
	var m domain.Member
	err := row.Scan(&m.UserID, &m.FamilyID, &m.Role, &m.Relation, &m.JoinedAt)
	return m, err
}

// AddMember добавляет пользователя в семью.
// ErrInFamily - пользователь уже в семье, ErrConflict - у семьи уже есть владелец.
func (s *Store) AddMember(ctx context.Context, m domain.Member) (domain.Member, error) {
	res, err := scanMember(s.conn(ctx).QueryRow(ctx, `
		INSERT INTO family_members AS m (user_id, family_id, role, relation)
		VALUES ($1, $2, $3, $4)
		RETURNING `+memberColumns,
		m.UserID, m.FamilyID, m.Role, m.Relation))
	return res, mapErr("add member", err)
}

// GetMembership возвращает членство пользователя (из него строится domain.Actor).
// ErrNotFound означает, что пользователь не состоит в семье.
func (s *Store) GetMembership(ctx context.Context, userID int64) (domain.Member, error) {
	m, err := scanMember(s.conn(ctx).QueryRow(ctx,
		`SELECT `+memberColumns+` FROM family_members m WHERE m.user_id = $1`, userID))
	return m, mapErr("get membership", err)
}

// ListMembers возвращает участников семьи: владелец первым, затем по дате вступления.
func (s *Store) ListMembers(ctx context.Context, familyID int64) ([]domain.MemberProfile, error) {
	rows, err := s.conn(ctx).Query(ctx, `
		SELECT `+memberColumns+`, u.display_name, u.username
		FROM family_members m
		JOIN users u ON u.telegram_id = m.user_id
		WHERE m.family_id = $1
		ORDER BY m.role = 'owner' DESC, m.joined_at, m.user_id`, familyID)
	if err != nil {
		return nil, mapErr("list members", err)
	}
	res, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.MemberProfile, error) {
		var p domain.MemberProfile
		err := row.Scan(&p.UserID, &p.FamilyID, &p.Role, &p.Relation, &p.JoinedAt, &p.DisplayName, &p.Username)
		return p, err
	})
	return res, mapErr("list members", err)
}

func (s *Store) CountMembers(ctx context.Context, familyID int64) (int, error) {
	var n int
	err := s.conn(ctx).QueryRow(ctx, `SELECT count(*) FROM family_members WHERE family_id = $1`, familyID).Scan(&n)
	return n, mapErr("count members", err)
}

func (s *Store) SetMemberRelation(ctx context.Context, familyID, userID int64, relation string) error {
	return s.execOne(ctx, "set member relation",
		`UPDATE family_members SET relation = $3 WHERE family_id = $1 AND user_id = $2`,
		familyID, userID, relation)
}

// SetMemberRole меняет роль. Передача владения: в одной транзакции сначала понизить
// текущего владельца, затем повысить нового (иначе сработает uniq_family_owner).
func (s *Store) SetMemberRole(ctx context.Context, familyID, userID int64, role domain.Role) error {
	return s.execOne(ctx, "set member role",
		`UPDATE family_members SET role = $3 WHERE family_id = $1 AND user_id = $2`,
		familyID, userID, role)
}

// RemoveMember удаляет участника из семьи. Его траты остаются в семье.
func (s *Store) RemoveMember(ctx context.Context, familyID, userID int64) error {
	return s.execOne(ctx, "remove member",
		`DELETE FROM family_members WHERE family_id = $1 AND user_id = $2`, familyID, userID)
}

// FamilyOwner возвращает владельца семьи (режим панели none: действия от его имени).
func (s *Store) FamilyOwner(ctx context.Context, familyID int64) (domain.Member, error) {
	m, err := scanMember(s.conn(ctx).QueryRow(ctx,
		`SELECT `+memberColumns+` FROM family_members m WHERE m.family_id = $1 AND m.role = 'owner'`, familyID))
	return m, mapErr("family owner", err)
}
