package category

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"expense-bot/internal/nlp/lexicon"
	"expense-bot/internal/nlp/textnorm"
	"expense-bot/internal/nlp/thesaurus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
)

// fakeThesaurus отвечает по таблице и считает запросы.
type fakeThesaurus struct {
	mu    sync.Mutex
	pages map[string]thesaurus.Related
	err   error
	calls int
}

func (f *fakeThesaurus) Lookup(_ context.Context, titles []string) (map[string]thesaurus.Related, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]thesaurus.Related{}
	for _, t := range titles {
		if r, ok := f.pages[t]; ok {
			out[t] = r
		}
	}
	return out, nil
}

// memStore - кэш выученных слов в памяти.
type memStore struct {
	mu    sync.Mutex
	terms map[string]domain.LearnedTerm
}

func newMemStore() *memStore { return &memStore{terms: map[string]domain.LearnedTerm{}} }

func (m *memStore) GetLearnedTerm(_ context.Context, stem string) (domain.LearnedTerm, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.terms[stem]
	if !ok {
		return domain.LearnedTerm{}, domain.ErrNotFound
	}
	return t, nil
}

func (m *memStore) SaveLearnedTerm(_ context.Context, t domain.LearnedTerm) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t.UpdatedAt = time.Now()
	m.terms[t.Stem] = t
	return nil
}

func categorize(c *Categorizer, words ...string) Guess {
	return c.Categorize(context.Background(), words)
}

func TestCategorizeDictionaryFirst(t *testing.T) {
	th := &fakeThesaurus{}
	c := NewCategorizer(testDict(t), newMemStore(), th, time.Second)
	g := categorize(c, "рестик")
	assert.Equal(t, SourceDictionary, g.Source)
	assert.Equal(t, domain.CategoryFood, g.Term.Category)
	assert.Zero(t, th.calls, "известное слово не идёт в сеть")
}

func TestCategorizeOnlineHypernymWins(t *testing.T) {
	th := &fakeThesaurus{pages: map[string]thesaurus.Related{"пельмень": {Hypernyms: []string{"блюдо"}}}}
	store := newMemStore()
	c := NewCategorizer(testDict(t), store, th, time.Second)

	g := categorize(c, "пельменями")
	require.True(t, g.Found())
	assert.Equal(t, SourceOnline, g.Source)
	assert.Equal(t, "пельмень", g.Term.Title, "название - начальная форма найденного слова")
	assert.Equal(t, domain.CategoryFood, g.Term.Category)

	// Второй раз - из кэша, без сети, и для другой формы слова тоже.
	calls := th.calls
	g = categorize(c, "пельмени")
	assert.Equal(t, SourceLearned, g.Source)
	assert.Equal(t, calls, th.calls)
}

func TestCategorizeSingleSynonymDoesNotDecide(t *testing.T) {
	// Регрессия: синоним «свет» у слова «день» уводил в электричество (жильё).
	th := &fakeThesaurus{pages: map[string]thesaurus.Related{"день": {
		Hypernyms: []string{"период", "время"},
		Synonyms:  []string{"свет", "сутки"},
	}}}
	store := newMemStore()
	c := NewCategorizer(testDict(t), store, th, time.Second)
	assert.False(t, categorize(c, "день").Found())

	// Слово запомнено как нераспознанное: повтор не идёт в сеть.
	calls := th.calls
	assert.False(t, categorize(c, "день").Found())
	assert.Equal(t, calls, th.calls)
}

func TestCategorizeAnimalBeatsRailwaySlang(t *testing.T) {
	// Регрессия: у «собаки» в Викисловаре гипероним «поезд» и синоним «электричка» (жаргон).
	// Обобщения про животных в словаре должны перевесить случайное значение.
	th := &fakeThesaurus{pages: map[string]thesaurus.Related{"лемур": {
		Hypernyms: []string{"млекопитающее", "зверь", "поезд"},
		Synonyms:  []string{"электричка"},
	}}}
	c := NewCategorizer(testDict(t), newMemStore(), th, time.Second)
	g := categorize(c, "лемур")
	require.True(t, g.Found())
	assert.Equal(t, domain.CategoryShopping, g.Term.Category)
}

func TestCategorizeSynonymsCanAgree(t *testing.T) {
	th := &fakeThesaurus{pages: map[string]thesaurus.Related{"днюха": {Synonyms: []string{"праздник", "юбилей"}}}}
	c := NewCategorizer(testDict(t), newMemStore(), th, time.Second)
	// «днюха» есть в словаре - проверяем на незнакомом слове с теми же синонимами.
	th.pages["гулянка"] = th.pages["днюха"]
	g := categorize(c, "гулянка")
	require.True(t, g.Found())
	assert.Equal(t, domain.CategoryEntertainment, g.Term.Category)
}

func TestCategorizeSecondHop(t *testing.T) {
	th := &fakeThesaurus{pages: map[string]thesaurus.Related{
		"флэтуайтик":       {Hypernyms: []string{"кофейный напиток"}},
		"кофейный напиток": {Hypernyms: []string{"напиток"}},
	}}
	c := NewCategorizer(testDict(t), newMemStore(), th, time.Second)
	g := categorize(c, "флэтуайтик")
	require.True(t, g.Found())
	assert.Equal(t, domain.CategoryFood, g.Term.Category)
}

func TestCategorizeBreakerStopsOnlineAfterFailures(t *testing.T) {
	th := &fakeThesaurus{err: errors.New("network down")}
	c := NewCategorizer(testDict(t), nil, th, time.Second)
	for range breakerFailures {
		assert.False(t, categorize(c, "фигнюшка").Found())
	}
	calls := th.calls
	assert.False(t, categorize(c, "фигнюшка").Found())
	assert.Equal(t, calls, th.calls, "после серии ошибок онлайн-поиск выключен на время")
}

func TestCategorizerLearnFromLLM(t *testing.T) {
	store := newMemStore()
	c := NewCategorizer(testDict(t), store, nil, time.Second)
	c.Learn(context.Background(), "Фрыкадэлька", lexicon.Term{Title: "фрикаделька", Category: domain.CategoryFood})
	g := categorize(c, "фрыкадэльки")
	assert.Equal(t, SourceLearned, g.Source)
	assert.Equal(t, "фрикаделька", g.Term.Title)

	c.Learn(context.Background(), "ерунда", lexicon.Term{Title: "ерунда", Category: "крипта"})
	_, err := store.GetLearnedTerm(context.Background(), textnorm.Stem("ерунда"))
	assert.ErrorIs(t, err, domain.ErrNotFound, "невалидная категория не сохраняется")
}

// testDict - настоящий словарь из config/synonyms.yaml: тесты ловят и ошибки в данных.
func testDict(t testing.TB) *lexicon.Dictionary {
	t.Helper()
	d, err := lexicon.LoadDictionary("../../../config/synonyms.yaml")
	require.NoError(t, err)
	return d
}
