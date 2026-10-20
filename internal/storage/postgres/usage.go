package postgres

import (
	"context"
	"time"

	"expense-bot/internal/llm"
)

// RecordUsage сохраняет попытку запроса к LLM (реализует llm.UsageRecorder).
// user_id и family_id = NULL, если запрос не от пользователя (служебный).
func (s *Store) RecordUsage(ctx context.Context, u llm.UsageRecord) error {
	_, err := s.conn(ctx).Exec(ctx, `
		INSERT INTO llm_usage (user_id, family_id, model, prompt_tokens, completion_tokens, latency_ms, ok)
		VALUES (NULLIF($1::bigint, 0), NULLIF($2::bigint, 0), $3, $4, $5, $6, $7)`,
		u.Caller.UserID, u.Caller.FamilyID, u.Provider+" "+u.Model, u.PromptTokens, u.CompletionTokens,
		u.Latency.Milliseconds(), u.OK)
	return mapErr("record llm usage", err)
}

// CountLLMUsage считает запросы к LLM с момента since: пользователя, семьи и всего.
func (s *Store) CountLLMUsage(ctx context.Context, userID, familyID int64, since time.Time) (user, family, total int, err error) {
	err = s.conn(ctx).QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE user_id = $1),
		       count(*) FILTER (WHERE family_id = $2),
		       count(*)
		FROM llm_usage WHERE created_at >= $3`, userID, familyID, since).Scan(&user, &family, &total)
	return user, family, total, mapErr("count llm usage", err)
}
