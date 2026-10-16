package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"expense-bot/internal/domain"
	"expense-bot/internal/storage/postgres"
)

const (
	MaxFamilyNameLen = 64
	MaxRelationLen   = 32
)

type FamiliesConfig struct {
	MaxMembers    int
	InviteTTL     time.Duration
	InviteMaxUses int
	DefaultTZ     string // APP_TZ, пояс новой семьи, если у владельца нет своего
}

// Families - семьи, участники, роли и приглашения.
type Families struct {
	store    *postgres.Store
	cfg      FamiliesConfig
	attempts *attemptLimiter
	now      func() time.Time
}

func NewFamilies(store *postgres.Store, cfg FamiliesConfig) *Families {
	return &Families{
		store:    store,
		cfg:      cfg,
		attempts: newAttemptLimiter(maxCodeFailures, codeFailureWindow, codeBlockDuration),
		now:      time.Now,
	}
}

// Actor строит domain.Actor по членству пользователя. ErrNotInFamily, если он не в семье.
func (f *Families) Actor(ctx context.Context, userID int64) (domain.Actor, error) {
	m, err := f.store.GetMembership(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Actor{}, fmt.Errorf("actor: %w", domain.ErrNotInFamily)
	}
	if err != nil {
		return domain.Actor{}, fmt.Errorf("actor: %w", err)
	}
	return domain.Actor{UserID: m.UserID, FamilyID: m.FamilyID, Role: m.Role}, nil
}

// CreateFamily создаёт семью с пользователем-владельцем и первым приглашением в одной транзакции.
// Пояс семьи берётся из пояса пользователя, если он уже задан, иначе APP_TZ.
func (f *Families) CreateFamily(ctx context.Context, userID int64, name string) (domain.Family, domain.Invite, error) {
	name, err := cleanText(name, MaxFamilyNameLen, "название семьи")
	if err != nil {
		return domain.Family{}, domain.Invite{}, err
	}
	var (
		fam domain.Family
		inv domain.Invite
	)
	err = f.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockUser(ctx, userID); err != nil {
			return err
		}
		if _, err := tx.GetMembership(ctx, userID); err == nil {
			return domain.ErrInFamily
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		u, err := tx.GetUser(ctx, userID)
		if err != nil {
			return err
		}
		tz := f.cfg.DefaultTZ
		if u.Timezone != "" {
			tz = u.Timezone
		}
		if fam, err = tx.CreateFamily(ctx, name, tz); err != nil {
			return err
		}
		if _, err = tx.AddMember(ctx, domain.Member{UserID: userID, FamilyID: fam.ID, Role: domain.RoleOwner}); err != nil {
			return err
		}
		inv, err = f.createInvite(ctx, tx, fam.ID, userID)
		return err
	})
	if err != nil {
		return domain.Family{}, domain.Invite{}, fmt.Errorf("create family: %w", err)
	}
	return fam, inv, nil
}

// JoinByCode вступает в семью по коду из deep link или ручного ввода.
// Ошибки: ErrTooManyAttempts (лимит неудачных попыток), ErrInviteInvalid (единая для
// неверного, истёкшего, отозванного и исчерпанного кода), ErrInFamily, ErrFamilyFull.
func (f *Families) JoinByCode(ctx context.Context, userID int64, rawCode string) (domain.Family, error) {
	now := f.now()
	if f.attempts.Blocked(userID, now) {
		return domain.Family{}, fmt.Errorf("join: %w", domain.ErrTooManyAttempts)
	}
	code, ok := NormalizeInviteCode(rawCode)
	if !ok {
		f.attempts.Fail(userID, now)
		return domain.Family{}, fmt.Errorf("join: %w", domain.ErrInviteInvalid)
	}

	var fam domain.Family
	err := f.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockUser(ctx, userID); err != nil {
			return err
		}
		// Проверяем до траты использования кода: состоящему в семье нужно сначала выйти.
		if _, err := tx.GetMembership(ctx, userID); err == nil {
			return domain.ErrInFamily
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		inv, err := tx.UseInvite(ctx, code)
		if err != nil {
			return err
		}
		n, err := tx.CountMembers(ctx, inv.FamilyID)
		if err != nil {
			return err
		}
		if n >= f.cfg.MaxMembers {
			return domain.ErrFamilyFull // откат вернёт использование приглашения
		}
		if _, err := tx.AddMember(ctx, domain.Member{UserID: userID, FamilyID: inv.FamilyID, Role: domain.RoleMember}); err != nil {
			return err
		}
		fam, err = tx.GetFamily(ctx, inv.FamilyID)
		return err
	})
	if errors.Is(err, domain.ErrInviteInvalid) {
		f.attempts.Fail(userID, now)
	}
	if err != nil {
		return domain.Family{}, fmt.Errorf("join: %w", err)
	}
	f.attempts.Reset(userID)
	return fam, nil
}

// GetFamily доступна любому участнику своей семьи.
func (f *Families) GetFamily(ctx context.Context, a domain.Actor) (domain.Family, error) {
	fam, err := f.store.GetFamily(ctx, a.FamilyID)
	if err != nil {
		return domain.Family{}, fmt.Errorf("get family: %w", err)
	}
	return fam, nil
}

// UpdateFamily меняет название и пояс семьи. Только владелец.
func (f *Families) UpdateFamily(ctx context.Context, a domain.Actor, name, tz string) (domain.Family, error) {
	if err := requireOwner(a); err != nil {
		return domain.Family{}, fmt.Errorf("update family: %w", err)
	}
	name, err := cleanText(name, MaxFamilyNameLen, "название семьи")
	if err != nil {
		return domain.Family{}, err
	}
	if _, err := time.LoadLocation(tz); err != nil || tz == "" {
		return domain.Family{}, fmt.Errorf("%w: неизвестный часовой пояс %q", domain.ErrValidation, tz)
	}
	fam, err := f.store.UpdateFamily(ctx, a.FamilyID, name, tz)
	if err != nil {
		return domain.Family{}, fmt.Errorf("update family: %w", err)
	}
	return fam, nil
}

// Leave - выход из семьи. Траты остаются в семье, пользователь возвращается на выбор семьи.
// Владелец выйти не может, пока в семье есть другие (ErrOwnerMustStay); последний участник
// удаляет семью целиком вместе с тратами.
func (f *Families) Leave(ctx context.Context, a domain.Actor) (familyDeleted bool, err error) {
	err = f.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockUser(ctx, a.UserID); err != nil {
			return err
		}
		// Роль перечитываем под блокировкой: Actor мог устареть (передача владения).
		m, err := tx.GetMembership(ctx, a.UserID)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && m.FamilyID != a.FamilyID) {
			return domain.ErrNotInFamily
		}
		if err != nil {
			return err
		}
		if m.Role == domain.RoleOwner {
			n, err := tx.CountMembers(ctx, a.FamilyID)
			if err != nil {
				return err
			}
			if n > 1 {
				return domain.ErrOwnerMustStay
			}
			familyDeleted = true
			if err := tx.DeleteFamily(ctx, a.FamilyID); err != nil {
				return err
			}
		} else if err := tx.RemoveMember(ctx, a.FamilyID, a.UserID); err != nil {
			return err
		}
		return detachUser(ctx, tx, a.UserID)
	})
	if err != nil {
		return false, fmt.Errorf("leave family: %w", err)
	}
	return familyDeleted, nil
}

// detachUser выполняется, когда пользователь перестал быть участником семьи:
// сессии панели инвалидируются, онбординг возвращается на выбор семьи.
func detachUser(ctx context.Context, tx *postgres.Store, userID int64) error {
	if err := tx.DeleteUserSessions(ctx, userID); err != nil {
		return err
	}
	return tx.SetUserState(ctx, userID, domain.StateAwaitingChoice, nil)
}

func requireOwner(a domain.Actor) error {
	if !a.IsOwner() {
		return domain.ErrForbidden
	}
	return nil
}

// cleanText обрезает пробелы и проверяет длину в символах.
func cleanText(s string, maxLen int, field string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxLen {
		return "", fmt.Errorf("%w: %s длиннее %d символов", domain.ErrValidation, field, maxLen)
	}
	return s, nil
}
