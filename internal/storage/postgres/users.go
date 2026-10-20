package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"expense-bot/internal/domain"
)

const userColumns = `telegram_id, display_name, username, timezone, state, state_data, is_active, created_at`

func scanUser(row pgx.Row) (domain.User, error) {
	var (
		u  domain.User
		tz *string
	)
	err := row.Scan(&u.TelegramID, &u.DisplayName, &u.Username, &tz, &u.State, &u.StateData, &u.IsActive, &u.CreatedAt)
	if tz != nil {
		u.Timezone = *tz
	}
	return u, err
}

// UpsertUser создаёт пользователя при первом сообщении или обновляет имя и username.
// Состояние онбординга и пояс не трогает. Возвращает актуальную запись.
func (s *Store) UpsertUser(ctx context.Context, telegramID int64, displayName, username string) (domain.User, error) {
	u, err := scanUser(s.conn(ctx).QueryRow(ctx, `
		INSERT INTO users (telegram_id, display_name, username)
		VALUES ($1, $2, $3)
		ON CONFLICT (telegram_id) DO UPDATE
		SET display_name = EXCLUDED.display_name, username = EXCLUDED.username, is_active = true
		RETURNING `+userColumns,
		telegramID, displayName, username))
	return u, mapErr("upsert user", err)
}

func (s *Store) GetUser(ctx context.Context, telegramID int64) (domain.User, error) {
	u, err := scanUser(s.conn(ctx).QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE telegram_id = $1`, telegramID))
	return u, mapErr("get user", err)
}

// SetUserState переводит пользователя на шаг онбординга. data = nil очищает state_data.
func (s *Store) SetUserState(ctx context.Context, telegramID int64, state domain.UserState, data json.RawMessage) error {
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	return s.execOne(ctx, "set user state",
		`UPDATE users SET state = $2, state_data = $3 WHERE telegram_id = $1`, telegramID, state, data)
}

// SetUserTimezone задаёт IANA-пояс. Пустая строка сбрасывает пояс на пояс семьи.
func (s *Store) SetUserTimezone(ctx context.Context, telegramID int64, tz string) error {
	return s.execOne(ctx, "set user timezone",
		`UPDATE users SET timezone = NULLIF($2, '') WHERE telegram_id = $1`, telegramID, tz)
}

// SetUserActive помечает, что пользователь заблокировал бота (false) или вернулся (true).
func (s *Store) SetUserActive(ctx context.Context, telegramID int64, active bool) error {
	return s.execOne(ctx, "set user active",
		`UPDATE users SET is_active = $2 WHERE telegram_id = $1`, telegramID, active)
}

// execOne выполняет запрос, который обязан затронуть ровно одну строку, иначе ErrNotFound.
func (s *Store) execOne(ctx context.Context, op, sql string, args ...any) error {
	tag, err := s.conn(ctx).Exec(ctx, sql, args...)
	if err != nil {
		return mapErr(op, err)
	}
	if tag.RowsAffected() == 0 {
		return mapErr(op, pgx.ErrNoRows)
	}
	return nil
}
