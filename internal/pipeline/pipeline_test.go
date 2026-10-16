package pipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
)

// Общие помощники тестов пакета.

// testDict - настоящий словарь из config/synonyms.yaml: тесты ловят и ошибки в данных.
func testDict(t testing.TB) *Dictionary {
	t.Helper()
	d, err := LoadDictionary("../../config/synonyms.yaml")
	require.NoError(t, err)
	return d
}

// testAnalyzer - анализатор без онлайн-тезауруса и без БД.
func testAnalyzer(t testing.TB) *Analyzer {
	t.Helper()
	d := testDict(t)
	return NewAnalyzer(d, NewCategorizer(d, nil, nil, 0), 300)
}

// today - пятница 16.10.2026.
var today = date("2026-10-16")

func date(s string) domain.Date {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func rub(r int64) domain.Money { return domain.Money(r * 100) }

func analyze(t testing.TB, a *Analyzer, text string) Decision {
	t.Helper()
	return a.Analyze(context.Background(), text, today)
}

func TestDictionaryFileIsValid(t *testing.T) {
	d := testDict(t)
	assert.Greater(t, d.Size(), 800, "словарь должен быть большим")
}
