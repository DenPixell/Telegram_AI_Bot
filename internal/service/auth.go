package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"expense-bot/internal/domain"
)

const (
	LoginTokenTTL = 10 * time.Minute
	SessionTTL    = 7 * 24 * time.Hour
	secretBytes   = 32
)

// ErrUnauthenticated - нет действующей сессии или токен входа недействителен.
var ErrUnauthenticated = errors.New("unauthenticated")

// Auth - вход владельца в панель по одноразовой ссылке и сессии (PANEL_AUTH_MODE=magic_link).
// Секреты отдаются клиенту, в БД хранятся только их SHA-256.
type Auth struct {
	store AuthStore
	now   func() time.Time
}

func NewAuth(store AuthStore) *Auth {
	return &Auth{store: store, now: time.Now}
}

// Session - выданная сессия. ID кладётся в HttpOnly cookie и больше никуда.
type Session struct {
	ID        string
	Actor     domain.Actor
	ExpiresAt time.Time
}

// CreateLoginToken выдаёт одноразовый токен для ссылки входа. Только владелец.
func (a *Auth) CreateLoginToken(ctx context.Context, actor domain.Actor) (token string, expiresAt time.Time, err error) {
	if err := requireOwner(actor); err != nil {
		return "", time.Time{}, fmt.Errorf("create login token: %w", err)
	}
	token, hash, err := newSecret()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = a.now().Add(LoginTokenTTL)
	if err := a.store.CreateLoginToken(ctx, hash, actor.UserID, expiresAt); err != nil {
		return "", time.Time{}, fmt.Errorf("create login token: %w", err)
	}
	return token, expiresAt, nil
}

// Exchange обменивает токен входа на сессию. Токен одноразовый; пользователь на момент обмена
// должен оставаться владельцем семьи.
func (a *Auth) Exchange(ctx context.Context, token string) (Session, error) {
	var sess Session
	err := a.store.WithTx(ctx, func(ctx context.Context) error {
		userID, err := a.store.UseLoginToken(ctx, hashSecret(token))
		if errors.Is(err, domain.ErrNotFound) {
			return ErrUnauthenticated
		}
		if err != nil {
			return err
		}
		actor, err := ownerActor(ctx, a.store, userID)
		if err != nil {
			return err
		}
		id, hash, err := newSecret()
		if err != nil {
			return err
		}
		sess = Session{ID: id, Actor: actor, ExpiresAt: a.now().Add(SessionTTL)}
		return a.store.CreateSession(ctx, hash, userID, sess.ExpiresAt)
	})
	if err != nil {
		return Session{}, fmt.Errorf("exchange login token: %w", err)
	}
	return sess, nil
}

// SessionActor возвращает владельца по идентификатору сессии из cookie. Роль перепроверяется
// при каждом запросе, поэтому потеря владения закрывает доступ сразу.
func (a *Auth) SessionActor(ctx context.Context, sessionID string) (domain.Actor, error) {
	if sessionID == "" {
		return domain.Actor{}, ErrUnauthenticated
	}
	userID, err := a.store.SessionUser(ctx, hashSecret(sessionID))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Actor{}, ErrUnauthenticated
	}
	if err != nil {
		return domain.Actor{}, fmt.Errorf("session: %w", err)
	}
	actor, err := ownerActor(ctx, a.store, userID)
	if err != nil {
		return domain.Actor{}, fmt.Errorf("session: %w", err)
	}
	return actor, nil
}

func (a *Auth) Logout(ctx context.Context, sessionID string) error {
	if err := a.store.DeleteSession(ctx, hashSecret(sessionID)); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// CSRFToken выводится из идентификатора сессии, поэтому его не нужно хранить:
// клиент получает его в ответе API и шлёт в заголовке X-CSRF-Token, а cookie сессии
// остаётся HttpOnly.
func CSRFToken(sessionID string) string {
	sum := sha256.Sum256([]byte("csrf:" + sessionID))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// PanelOwner - действия в режиме PANEL_AUTH_MODE=none: от имени владельца семьи familyID
// (0 = единственная существующая семья).
func PanelOwner(ctx context.Context, store PanelStore, familyID int64) (domain.Actor, error) {
	if familyID == 0 {
		id, err := store.SoleFamilyID(ctx)
		if err != nil {
			return domain.Actor{}, fmt.Errorf("panel family: %w", err)
		}
		familyID = id
	}
	m, err := store.FamilyOwner(ctx, familyID)
	if err != nil {
		return domain.Actor{}, fmt.Errorf("panel family owner: %w", err)
	}
	return domain.Actor{UserID: m.UserID, FamilyID: m.FamilyID, Role: m.Role}, nil
}

// ownerActor строит Actor и требует роль владельца: панель доступна только ему.
func ownerActor(ctx context.Context, s membershipReader, userID int64) (domain.Actor, error) {
	m, err := s.GetMembership(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Actor{}, domain.ErrForbidden
	}
	if err != nil {
		return domain.Actor{}, err
	}
	if m.Role != domain.RoleOwner {
		return domain.Actor{}, domain.ErrForbidden
	}
	return domain.Actor{UserID: m.UserID, FamilyID: m.FamilyID, Role: m.Role}, nil
}

// newSecret возвращает случайный секрет для клиента и его SHA-256 для БД.
func newSecret() (secret string, hash []byte, err error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generate secret: %w", err)
	}
	secret = base64.RawURLEncoding.EncodeToString(b)
	return secret, hashSecret(secret), nil
}

func hashSecret(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
