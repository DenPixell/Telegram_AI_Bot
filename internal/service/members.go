package service

import (
	"context"
	"errors"
	"fmt"

	"expense-bot/internal/domain"
	"expense-bot/internal/storage/postgres"
)

// ListMembers возвращает участников семьи (владелец первым). Только владелец.
func (f *Families) ListMembers(ctx context.Context, a domain.Actor) ([]domain.MemberProfile, error) {
	if err := requireOwner(a); err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	ms, err := f.store.ListMembers(ctx, a.FamilyID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	return ms, nil
}

// SetRelation задаёт подпись участника («жена», «сын»). На права не влияет. Только владелец.
func (f *Families) SetRelation(ctx context.Context, a domain.Actor, userID int64, relation string) error {
	if err := requireOwner(a); err != nil {
		return fmt.Errorf("set relation: %w", err)
	}
	relation, err := cleanText(relation, MaxRelationLen, "relation")
	if err != nil {
		return err
	}
	if err := f.store.SetMemberRelation(ctx, a.FamilyID, userID, relation); err != nil {
		return fmt.Errorf("set relation: %w", err)
	}
	return nil
}

// RemoveMember удаляет участника из семьи. Его траты остаются в семье, сессии инвалидируются.
// Себя удалить нельзя: для этого Leave или передача владения. Только владелец.
func (f *Families) RemoveMember(ctx context.Context, a domain.Actor, userID int64) error {
	if err := requireOwner(a); err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if userID == a.UserID {
		return fmt.Errorf("remove member: %w: owner cannot remove themselves", domain.ErrValidation)
	}
	err := f.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockUser(ctx, userID); err != nil {
			return err
		}
		if err := tx.RemoveMember(ctx, a.FamilyID, userID); err != nil {
			return err
		}
		return detachUser(ctx, tx, userID)
	})
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

// TransferOwnership передаёт владение другому участнику своей семьи. Сессии обоих
// инвалидируются, потому что меняются права. Только владелец.
func (f *Families) TransferOwnership(ctx context.Context, a domain.Actor, newOwnerID int64) error {
	if err := requireOwner(a); err != nil {
		return fmt.Errorf("transfer ownership: %w", err)
	}
	if newOwnerID == a.UserID {
		return fmt.Errorf("transfer ownership: %w: already the owner", domain.ErrValidation)
	}
	err := f.store.WithTx(ctx, func(tx *postgres.Store) error {
		// Блокировки в порядке возрастания id, чтобы два встречных вызова не взаимоблокировались.
		first, second := min(a.UserID, newOwnerID), max(a.UserID, newOwnerID)
		if err := tx.LockUser(ctx, first); err != nil {
			return err
		}
		if err := tx.LockUser(ctx, second); err != nil {
			return err
		}
		// Понизить текущего владельца первым, иначе сработает uniq_family_owner.
		// execOne с family_id гарантирует, что оба в этой семье и Actor не устарел.
		if err := tx.SetMemberRole(ctx, a.FamilyID, a.UserID, domain.RoleMember); err != nil {
			return err
		}
		if err := tx.SetMemberRole(ctx, a.FamilyID, newOwnerID, domain.RoleOwner); err != nil {
			return err
		}
		if err := tx.DeleteUserSessions(ctx, a.UserID); err != nil {
			return err
		}
		return tx.DeleteUserSessions(ctx, newOwnerID)
	})
	if errors.Is(err, domain.ErrConflict) {
		// Владелец уже сменился параллельно: Actor устарел.
		return fmt.Errorf("transfer ownership: %w", domain.ErrForbidden)
	}
	if err != nil {
		return fmt.Errorf("transfer ownership: %w", err)
	}
	return nil
}
