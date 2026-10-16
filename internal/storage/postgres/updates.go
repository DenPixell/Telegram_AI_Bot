package postgres

import "context"

// MarkUpdateProcessed отмечает апдейт Telegram обработанным. false - он уже был
// (повторная доставка после рестарта): обрабатывать второй раз нельзя.
func (s *Store) MarkUpdateProcessed(ctx context.Context, updateID int64) (bool, error) {
	tag, err := s.q.Exec(ctx,
		`INSERT INTO processed_updates (update_id) VALUES ($1) ON CONFLICT DO NOTHING`, updateID)
	if err != nil {
		return false, mapErr("mark update", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DeleteOldUpdates чистит журнал дедупликации старше двух суток.
func (s *Store) DeleteOldUpdates(ctx context.Context) (int64, error) {
	tag, err := s.q.Exec(ctx, `DELETE FROM processed_updates WHERE processed_at < now() - interval '2 days'`)
	if err != nil {
		return 0, mapErr("delete old updates", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteUserData удаляет все данные пользователя («удалить мои данные»): траты во всех
// семьях, созданные им приглашения, сессии, токены входа, учёт LLM, членство и профиль.
// Вызывать в транзакции после решения о семье (см. service.Families.DeleteMyData).
func (s *Store) DeleteUserData(ctx context.Context, userID int64) error {
	for _, q := range []string{
		`DELETE FROM expenses WHERE user_id = $1`,
		`DELETE FROM invites WHERE created_by = $1`,
		`DELETE FROM sessions WHERE user_id = $1`,
		`DELETE FROM login_tokens WHERE user_id = $1`,
		`DELETE FROM llm_usage WHERE user_id = $1`,
		`DELETE FROM family_members WHERE user_id = $1`,
		`DELETE FROM users WHERE telegram_id = $1`,
	} {
		if _, err := s.q.Exec(ctx, q, userID); err != nil {
			return mapErr("delete user data", err)
		}
	}
	return nil
}
