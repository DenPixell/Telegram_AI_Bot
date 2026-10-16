package postgres

import (
	"context"
	"time"
)

// LLMUsage - один запрос к LLM для учёта квот и стоимости.
type LLMUsage struct {
	UserID, FamilyID int64
	Model            string
	PromptTokens     int
	CompletionTokens int
	Latency          time.Duration
	OK               bool
}

func (s *Store) RecordLLMUsage(ctx context.Context, u LLMUsage) error {
	_, err := s.q.Exec(ctx, `
		INSERT INTO llm_usage (user_id, family_id, model, prompt_tokens, completion_tokens, latency_ms, ok)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		u.UserID, u.FamilyID, u.Model, u.PromptTokens, u.CompletionTokens, u.Latency.Milliseconds(), u.OK)
	return mapErr("record llm usage", err)
}

// CountLLMUsage считает запросы к LLM с момента since: пользователя, семьи и всего.
func (s *Store) CountLLMUsage(ctx context.Context, userID, familyID int64, since time.Time) (user, family, total int, err error) {
	err = s.q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE user_id = $1),
		       count(*) FILTER (WHERE family_id = $2),
		       count(*)
		FROM llm_usage WHERE created_at >= $3`, userID, familyID, since).Scan(&user, &family, &total)
	return user, family, total, mapErr("count llm usage", err)
}
