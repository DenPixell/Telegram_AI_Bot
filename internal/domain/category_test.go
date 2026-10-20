package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
)

func TestNewCategorySetValidates(t *testing.T) {
	food := domain.CategoryInfo{Name: "еда", Position: 1}
	other := domain.CategoryInfo{Name: "другое", Position: 2, Fallback: true}
	tests := []struct {
		name    string
		list    []domain.CategoryInfo
		wantErr string
	}{
		{"корректный набор", []domain.CategoryInfo{food, other}, ""},
		{"пустой набор", nil, "no categories"},
		{"без запасной", []domain.CategoryInfo{food}, "no fallback"},
		{"две запасные", []domain.CategoryInfo{other, {Name: "прочее", Fallback: true}}, "two fallback"},
		{"дубликат", []domain.CategoryInfo{food, food, other}, "duplicate"},
		{"пустое имя", []domain.CategoryInfo{{Name: " "}, other}, "empty name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.NewCategorySet(tt.list)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestCategorySetLookup(t *testing.T) {
	s, err := domain.NewCategorySet([]domain.CategoryInfo{
		{Name: "еда", Position: 1}, {Name: "питомцы", Position: 2}, {Name: "другое", Position: 3, Fallback: true},
	})
	require.NoError(t, err)

	assert.True(t, s.Valid("питомцы"), "новая категория из БД работает без изменений кода")
	assert.False(t, s.Valid("крипта"))
	assert.Equal(t, domain.Category("еда"), s.Parse("еда"))
	assert.Equal(t, domain.Category("другое"), s.Parse("крипта"), "выдуманная моделью - в запасную")
	assert.Equal(t, domain.Category("другое"), s.Fallback())
	assert.Equal(t, []string{"еда", "питомцы", "другое"}, s.Names())
}
