package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
	"expense-bot/internal/testutil/pgtest"
	"expense-bot/internal/testutil/testcats"
)

func TestSeedCategoriesMatchTestSet(t *testing.T) {
	s, _ := pgtest.New(t)
	list, err := s.ListCategories(ctx)
	require.NoError(t, err)
	assert.Equal(t, testcats.List(), list, "начальный набор миграции и testcats должны совпадать")

	set, err := s.Categories(ctx)
	require.NoError(t, err)
	assert.Equal(t, testcats.Other, set.Fallback())
}

func TestNewCategoryWithoutMigration(t *testing.T) {
	s, pool := pgtest.New(t)
	f := family(t, s, "Ивановы", 1)

	_, err := s.CreateExpense(ctx, domain.Expense{
		FamilyID: f.ID, UserID: 1, Amount: 50000, Category: "питомцы", SpentAt: d("2026-10-15"),
	})
	require.ErrorIs(t, err, domain.ErrValidation, "неизвестную категорию отклоняет внешний ключ")

	// Новая категория - просто строка в справочнике, без изменения схемы.
	_, err = pool.Exec(ctx, `INSERT INTO categories (name, title, emoji, position) VALUES ('питомцы', 'Питомцы', '🐾', 8)`)
	require.NoError(t, err)
	set, err := s.Categories(ctx)
	require.NoError(t, err)
	assert.True(t, set.Valid("питомцы"))
	assert.Equal(t, "питомцы", set.Names()[len(set.Names())-1], "порядок по position")

	e := expense(t, s, f.ID, 1, 500, "питомцы", "2026-10-15", "корм")
	assert.Equal(t, domain.Category("питомцы"), e.Category)

	// Категорию с тратами удалить нельзя - траты не теряют категорию.
	_, err = pool.Exec(ctx, `DELETE FROM categories WHERE name = 'питомцы'`)
	assert.Error(t, err)
}

func TestSecondFallbackCategoryRejected(t *testing.T) {
	_, pool := pgtest.New(t)
	_, err := pool.Exec(ctx, `INSERT INTO categories (name, title, position, is_fallback) VALUES ('прочее', 'Прочее', 9, true)`)
	assert.Error(t, err, "запасная категория ровно одна")
}
