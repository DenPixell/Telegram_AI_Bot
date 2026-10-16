package postgres

import (
	"context"

	"expense-bot/internal/domain"
)

// GetLearnedTerm возвращает выученное слово по основе и увеличивает счётчик обращений.
// ErrNotFound - слова нет в кэше.
func (s *Store) GetLearnedTerm(ctx context.Context, stem string) (domain.LearnedTerm, error) {
	var (
		t   domain.LearnedTerm
		cat *string
	)
	err := s.q.QueryRow(ctx, `
		UPDATE learned_terms SET hits = hits + 1 WHERE stem = $1
		RETURNING stem, word, title, category, source, updated_at`, stem).
		Scan(&t.Stem, &t.Word, &t.Title, &cat, &t.Source, &t.UpdatedAt)
	if cat != nil {
		t.Category = domain.Category(*cat)
	}
	return t, mapErr("get learned term", err)
}

// SaveLearnedTerm сохраняет или обновляет слово. Category "" - отрицательный кэш.
func (s *Store) SaveLearnedTerm(ctx context.Context, t domain.LearnedTerm) error {
	_, err := s.q.Exec(ctx, `
		INSERT INTO learned_terms (stem, word, title, category, source)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		ON CONFLICT (stem) DO UPDATE
		SET word = EXCLUDED.word, title = EXCLUDED.title, category = EXCLUDED.category,
		    source = EXCLUDED.source, updated_at = now()`,
		t.Stem, t.Word, t.Title, string(t.Category), t.Source)
	return mapErr("save learned term", err)
}
