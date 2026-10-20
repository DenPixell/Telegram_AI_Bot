package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"expense-bot/internal/domain"
)

// ListCategories возвращает справочник категорий в порядке отображения.
func (s *Store) ListCategories(ctx context.Context) ([]domain.CategoryInfo, error) {
	rows, err := s.conn(ctx).Query(ctx, `
		SELECT name, title, emoji, position, is_fallback FROM categories ORDER BY position, name`)
	if err != nil {
		return nil, mapErr("list categories", err)
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.CategoryInfo, error) {
		var c domain.CategoryInfo
		err := r.Scan(&c.Name, &c.Title, &c.Emoji, &c.Position, &c.Fallback)
		return c, err
	})
	return list, mapErr("list categories", err)
}

// Categories загружает и проверяет справочник категорий (при старте приложения).
func (s *Store) Categories(ctx context.Context) (*domain.CategorySet, error) {
	list, err := s.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	set, err := domain.NewCategorySet(list)
	if err != nil {
		return nil, fmt.Errorf("load categories: %w", err)
	}
	return set, nil
}
