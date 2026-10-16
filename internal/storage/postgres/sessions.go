package postgres

import (
	"context"
	"time"
)

// В БД хранятся только SHA-256 хеши токенов и идентификаторов сессий.

// CreateLoginToken сохраняет одноразовый токен входа в панель.
func (s *Store) CreateLoginToken(ctx context.Context, tokenHash []byte, userID int64, expiresAt time.Time) error {
	_, err := s.q.Exec(ctx,
		`INSERT INTO login_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt)
	return mapErr("create login token", err)
}

// UseLoginToken атомарно помечает токен использованным и возвращает пользователя.
// Неизвестный, истёкший и уже использованный токен дают ErrNotFound.
func (s *Store) UseLoginToken(ctx context.Context, tokenHash []byte) (int64, error) {
	var userID int64
	err := s.q.QueryRow(ctx, `
		UPDATE login_tokens SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id`, tokenHash).Scan(&userID)
	return userID, mapErr("use login token", err)
}

func (s *Store) CreateSession(ctx context.Context, idHash []byte, userID int64, expiresAt time.Time) error {
	_, err := s.q.Exec(ctx,
		`INSERT INTO sessions (id_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		idHash, userID, expiresAt)
	return mapErr("create session", err)
}

// SessionUser возвращает пользователя действующей сессии. ErrNotFound - сессии нет или она истекла.
func (s *Store) SessionUser(ctx context.Context, idHash []byte) (int64, error) {
	var userID int64
	err := s.q.QueryRow(ctx,
		`SELECT user_id FROM sessions WHERE id_hash = $1 AND expires_at > now()`, idHash).Scan(&userID)
	return userID, mapErr("get session", err)
}

func (s *Store) DeleteSession(ctx context.Context, idHash []byte) error {
	_, err := s.q.Exec(ctx, `DELETE FROM sessions WHERE id_hash = $1`, idHash)
	return mapErr("delete session", err)
}

// DeleteUserSessions инвалидирует все сессии панели пользователя
// (смена роли, удаление из семьи, выход).
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.q.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return mapErr("delete user sessions", err)
}

// DeleteExpiredAuth чистит истёкшие токены входа и сессии. Вызывается по расписанию.
func (s *Store) DeleteExpiredAuth(ctx context.Context) (int64, error) {
	t1, err := s.q.Exec(ctx, `DELETE FROM login_tokens WHERE expires_at < now() - interval '1 day'`)
	if err != nil {
		return 0, mapErr("delete expired login tokens", err)
	}
	t2, err := s.q.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	if err != nil {
		return 0, mapErr("delete expired sessions", err)
	}
	return t1.RowsAffected() + t2.RowsAffected(), nil
}
