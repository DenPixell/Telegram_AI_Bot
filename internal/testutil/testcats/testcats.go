// Package testcats - справочник категорий для тестов, такой же, как начальный набор
// миграции 00004_categories.sql (совпадение проверяет TestSeedCategoriesMatchTestSet
// в storage/postgres). В рабочем коде категории читаются из БД.
package testcats

import "expense-bot/internal/domain"

const (
	Food          domain.Category = "еда"
	Transport     domain.Category = "транспорт"
	Housing       domain.Category = "жильё"
	Entertainment domain.Category = "развлечения"
	Health        domain.Category = "здоровье"
	Shopping      domain.Category = "покупки"
	Other         domain.Category = "другое"
)

// List - начальный набор в порядке отображения.
func List() []domain.CategoryInfo {
	return []domain.CategoryInfo{
		{Name: Food, Title: "Еда", Emoji: "🍽️", Position: 1},
		{Name: Transport, Title: "Транспорт", Emoji: "🚕", Position: 2},
		{Name: Housing, Title: "Жильё", Emoji: "🏠", Position: 3},
		{Name: Entertainment, Title: "Развлечения", Emoji: "🎉", Position: 4},
		{Name: Health, Title: "Здоровье", Emoji: "💊", Position: 5},
		{Name: Shopping, Title: "Покупки", Emoji: "🛍️", Position: 6},
		{Name: Other, Title: "Другое", Emoji: "📦", Position: 7, Fallback: true},
	}
}

// Set - тот же набор, готовый к использованию.
func Set() *domain.CategorySet {
	s, err := domain.NewCategorySet(List())
	if err != nil {
		panic(err)
	}
	return s
}
