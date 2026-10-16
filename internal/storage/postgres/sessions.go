package postgres

import "context"

// TODO(этап 4): CreateLoginToken, ExchangeLoginToken, CreateSession, GetSession, чистка истёкших.

// DeleteUserSessions инвалидирует все сессии панели пользователя
// (смена роли, удаление из семьи, выход).
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	if _, err := s.q.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return mapErr("delete user sessions", err)
	}
	return nil
}
