package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"

	"expense-bot/internal/domain"
	"expense-bot/internal/storage/postgres"
)

// Коды приглашений: Crockford base32 (без I, L, O, U), 10 символов = 50 бит энтропии.
// Один и тот же код используется в deep link (?start=CODE) и при ручном вводе.
const (
	inviteAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	InviteCodeLen  = 10
)

// NewInviteCode генерирует код из crypto/rand. Уникальность обеспечивает UNIQUE (code)
// в БД, вызывающий повторяет генерацию при нарушении ограничения.
func NewInviteCode() (string, error) {
	b := make([]byte, InviteCodeLen)
	max := big.NewInt(int64(len(inviteAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = inviteAlphabet[n.Int64()]
	}
	return string(b), nil
}

// NormalizeInviteCode приводит ручной ввод к каноническому виду: верхний регистр,
// O->0, I/L->1, без пробелов и дефисов. ok=false, если код не может быть валидным.
func NormalizeInviteCode(s string) (code string, ok bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case ' ', '-', '\t':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		if !strings.ContainsRune(inviteAlphabet, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	code = b.String()
	return code, len(code) == InviteCodeLen
}

// FormatInviteCode возвращает вид для показа человеку: K7M2Q-9XH4T.
func FormatInviteCode(code string) string {
	if len(code) != InviteCodeLen {
		return code
	}
	return code[:5] + "-" + code[5:]
}

// InviteLink возвращает deep link для приглашения: https://t.me/<bot>?start=<CODE>.
// Код из алфавита [0-9A-Z] укладывается в ограничение Telegram на параметр start.
func InviteLink(botUsername, code string) string {
	return "https://t.me/" + url.PathEscape(strings.TrimPrefix(botUsername, "@")) + "?start=" + code
}

// maxCodeAttempts - сколько раз генерировать код при коллизии UNIQUE (code).
const maxCodeAttempts = 5

// CreateInvite создаёт приглашение с TTL и лимитом использований из конфига. Только владелец.
func (f *Families) CreateInvite(ctx context.Context, a domain.Actor) (domain.Invite, error) {
	if err := requireOwner(a); err != nil {
		return domain.Invite{}, fmt.Errorf("create invite: %w", err)
	}
	inv, err := f.createInvite(ctx, f.store, a.FamilyID, a.UserID)
	if err != nil {
		return domain.Invite{}, fmt.Errorf("create invite: %w", err)
	}
	return inv, nil
}

// createInvite генерирует код и повторяет при коллизии. Внутри транзакции каждая попытка
// идёт в своём savepoint, чтобы ошибка уникальности не прерывала всю транзакцию.
func (f *Families) createInvite(ctx context.Context, s *postgres.Store, familyID, createdBy int64) (domain.Invite, error) {
	for range maxCodeAttempts {
		code, err := NewInviteCode()
		if err != nil {
			return domain.Invite{}, fmt.Errorf("generate invite code: %w", err)
		}
		var inv domain.Invite
		err = s.WithTx(ctx, func(tx *postgres.Store) error {
			inv, err = tx.CreateInvite(ctx, domain.Invite{
				FamilyID:  familyID,
				Code:      code,
				CreatedBy: createdBy,
				ExpiresAt: f.now().Add(f.cfg.InviteTTL),
				MaxUses:   f.cfg.InviteMaxUses,
			})
			return err
		})
		if errors.Is(err, postgres.ErrDuplicateInviteCode) {
			continue
		}
		return inv, err
	}
	return domain.Invite{}, fmt.Errorf("invite code collision %d times in a row", maxCodeAttempts)
}

// ListInvites возвращает действующие приглашения семьи. Только владелец.
func (f *Families) ListInvites(ctx context.Context, a domain.Actor) ([]domain.Invite, error) {
	if err := requireOwner(a); err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	invs, err := f.store.ListActiveInvites(ctx, a.FamilyID)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	return invs, nil
}

// RevokeInvite отзывает приглашение своей семьи. Только владелец.
func (f *Families) RevokeInvite(ctx context.Context, a domain.Actor, inviteID int64) error {
	if err := requireOwner(a); err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	if err := f.store.RevokeInvite(ctx, a.FamilyID, inviteID); err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	return nil
}
