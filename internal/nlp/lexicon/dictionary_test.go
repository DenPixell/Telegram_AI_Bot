package lexicon

import (
	"testing"

	"expense-bot/internal/nlp/textnorm"
	"expense-bot/internal/testutil/testcats"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
)

func TestLookup(t *testing.T) {
	d := testDict(t)
	cases := []struct {
		text  string
		title string // "" - совпадений нет
		cat   domain.Category
	}{
		{"рестик", "ресторан", testcats.Food},
		{"в ресторане", "ресторан", testcats.Food},
		{"шавуху", "шаурма", testcats.Food},
		{"латте", "кофе", testcats.Food},
		{"в пятёрочке", "супермаркет", testcats.Food},
		{"вкусно и точка", "фастфуд", testcats.Food},  // фраза со служебным словом
		{"яндекс такси", "такси", testcats.Transport}, // многословный синоним
		{"аренда самоката", "самокат-аренда", testcats.Transport},
		{"аренда", "аренда квартиры", testcats.Housing},
		{"мандаринчики", "фрукты", testcats.Food}, // уменьшительная форма
		{"кофеечек", "кофе", testcats.Food},
		{"зефирка", "сладости", testcats.Food},
		{"носовые платки", "бытовая химия", testcats.Housing},
		{"день рождения", "праздник", testcats.Entertainment},
		{"купе", "поезд", testcats.Transport},
		{"собака", "зоотовары", testcats.Shopping}, // регрессия: «собака» -> «поезд» (жаргон) -> транспорт
		{"корм для кота", "зоотовары", testcats.Shopping},
		// Регрессии.
		{"купили", "", ""}, // служебное слово не совпадает с «купе»
		{"найм", "", ""},   // слишком общее слово
		{"день", "", ""},
		{"фигнюшка", "", ""},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			m := d.Lookup(textnorm.Words(textnorm.Normalize(c.text)))
			if c.title == "" {
				assert.Empty(t, m)
				return
			}
			require.NotEmpty(t, m)
			assert.Equal(t, c.title, m[0].Term.Title)
			assert.Equal(t, c.cat, m[0].Term.Category)
		})
	}
}

func TestLookupLongestMatchFirst(t *testing.T) {
	d := testDict(t)
	m := d.Lookup(textnorm.Words(textnorm.Normalize("маршрутное такси")))
	require.Len(t, m, 1)
	assert.Equal(t, "автобус", m[0].Term.Title, "«маршрутное такси» - не «такси»")
}

func TestExpand(t *testing.T) {
	d := testDict(t)
	forms := d.Expand("рестик")
	assert.Contains(t, forms, "рестик")
	assert.Contains(t, forms, "ресторан")
	assert.Contains(t, forms, "общепит")

	// Заголовок с «ё» тоже попадает в поиск: так хранятся траты.
	assert.Contains(t, d.Expand("квартира"), "жильё")

	// Незнакомое слово остаётся как есть.
	assert.Equal(t, []string{"фигнюшка"}, d.Expand("Фигнюшка"))
}

func TestParseDictionaryRejectsConflicts(t *testing.T) {
	_, err := ParseDictionary([]byte(`
terms:
  кофе: {category: еда, aliases: [донат]}
  благотворительность: {category: другое, aliases: [донат]}
`), testcats.Set())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "донат")
}

func TestParseDictionaryRejectsUnknownCategory(t *testing.T) {
	_, err := ParseDictionary([]byte(`terms: {кофе: {category: напитки}}`), testcats.Set())
	require.Error(t, err)
}

func TestStemNormalizesForms(t *testing.T) {
	for _, pair := range [][2]string{{"такси", "такси"}, {"аптека", "аптеку"}, {"продукты", "продуктов"}} {
		assert.Equal(t, textnorm.Stem(pair[0]), textnorm.Stem(pair[1]), pair)
	}
	assert.Equal(t, "netflix", textnorm.Stem("netflix"), "латиница не стеммится")
}

// Стеммер несимметричен на части слов («ресторан» -> «рестора», «ресторане» -> «ресторан»):
// словарь всё равно должен узнавать все формы.
func TestLookupAllCaseForms(t *testing.T) {
	d := testDict(t)
	for _, w := range []string{"ресторан", "ресторана", "ресторане", "ресторану", "ресторанов", "ресторанах", "ресторанами"} {
		m := d.Lookup([]string{w})
		if assert.NotEmpty(t, m, w) {
			assert.Equal(t, "ресторан", m[0].Term.Title, w)
		}
	}
}

// testDict - настоящий словарь из config/synonyms.yaml: тесты ловят и ошибки в данных.
func testDict(t testing.TB) *Dictionary {
	t.Helper()
	d, err := LoadDictionary("../../../config/synonyms.yaml", testcats.Set())
	require.NoError(t, err)
	return d
}
