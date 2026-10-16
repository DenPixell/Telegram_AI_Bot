package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"expense-bot/internal/domain"
)

// Source - откуда взята категория.
type Source string

const (
	SourceDictionary Source = "dictionary"
	SourceLearned    Source = "learned" // кэш в БД (ранее найдено онлайн или LLM)
	SourceOnline     Source = "online"
	SourceNone       Source = "" // не распознано, решает LLM
)

// LearnedStore - кэш выученных слов (postgres.Store).
type LearnedStore interface {
	GetLearnedTerm(ctx context.Context, stem string) (domain.LearnedTerm, error)
	SaveLearnedTerm(ctx context.Context, t domain.LearnedTerm) error
}

const (
	maxOnlineWords   = 2                   // сколько незнакомых слов сообщения проверять онлайн
	maxHypernymHops  = 3                   // сколько связанных слов проверять на втором шаге
	negativeCacheTTL = 30 * 24 * time.Hour // через сколько повторить онлайн-поиск для нераспознанного
	breakerFailures  = 3
	breakerCooldown  = 5 * time.Minute
)

// Guess - результат распознавания категории.
type Guess struct {
	Term   Term
	Source Source
}

func (g Guess) Found() bool { return g.Source != SourceNone }

// Categorizer - гибридное распознавание категории:
//  1. словарь config/synonyms.yaml (по основам слов);
//  2. кэш выученных слов в БД;
//  3. онлайн-тезаурус (гиперонимы и синонимы из Викисловаря), результат кэшируется;
//  4. не нашли - SourceNone, решение за LLM (после фильтра запретов).
type Categorizer struct {
	dict    *Dictionary
	store   LearnedStore // nil - без кэша
	thes    Thesaurus    // nil - без онлайн-поиска
	timeout time.Duration
	sem     chan struct{} // не больше 2 одновременных онлайн-запросов
	breaker breaker
	now     func() time.Time
}

func NewCategorizer(dict *Dictionary, store LearnedStore, thes Thesaurus, timeout time.Duration) *Categorizer {
	return &Categorizer{
		dict: dict, store: store, thes: thes, timeout: timeout,
		sem: make(chan struct{}, 2), now: time.Now,
		breaker: breaker{failures: breakerFailures, cooldown: breakerCooldown},
	}
}

// Categorize определяет термин по словам названия траты (без суммы, даты и служебных слов).
func (c *Categorizer) Categorize(ctx context.Context, words []string) Guess {
	if g, ok := c.fromDictionary(words); ok {
		return g
	}
	candidates := c.unknownWords(words)
	if len(candidates) == 0 {
		return Guess{}
	}

	var online []string
	for _, w := range candidates {
		if c.store == nil {
			online = append(online, w)
			continue
		}
		t, err := c.store.GetLearnedTerm(ctx, Stem(w))
		switch {
		case errors.Is(err, domain.ErrNotFound):
			online = append(online, w)
		case err != nil:
			slog.WarnContext(ctx, "learned term lookup failed", "err", err)
			online = append(online, w)
		case t.Category != "":
			return Guess{Term: Term{Title: t.Title, Category: t.Category}, Source: SourceLearned}
		case c.now().Sub(t.UpdatedAt) > negativeCacheTTL:
			online = append(online, w) // отрицательный кэш устарел
		}
	}
	for _, w := range online {
		if g, ok := c.fromThesaurus(ctx, w); ok {
			return g
		}
	}
	return Guess{}
}

// Learn сохраняет категорию, которую определила LLM, чтобы в следующий раз обойтись без неё.
func (c *Categorizer) Learn(ctx context.Context, word string, term Term) {
	if c.store == nil || !term.Category.Valid() {
		return
	}
	word = Normalize(word)
	if err := c.store.SaveLearnedTerm(ctx, domain.LearnedTerm{
		Stem: Stem(word), Word: word, Title: term.Title, Category: term.Category, Source: "llm",
	}); err != nil {
		slog.WarnContext(ctx, "save learned term failed", "err", err)
	}
}

func (c *Categorizer) fromDictionary(words []string) (Guess, bool) {
	matches := c.dict.Lookup(words)
	if len(matches) == 0 {
		return Guess{}, false
	}
	// Несколько терминов разных категорий («кофе и бензин 500») - неоднозначно.
	for _, m := range matches[1:] {
		if m.Term.Category != matches[0].Term.Category {
			return Guess{}, false
		}
	}
	return Guess{Term: matches[0].Term, Source: SourceDictionary}, true
}

// unknownWords - слова, которые имеет смысл искать онлайн: кириллица, от 3 до 30 букв,
// не служебные. Не больше maxOnlineWords.
func (c *Categorizer) unknownWords(words []string) []string {
	var out []string
	for _, w := range words {
		n := utf8.RuneCountInString(w)
		if n < 3 || n > 30 || !isCyrillic(w) || c.dict.fillers[w] {
			continue
		}
		out = append(out, w)
		if len(out) == maxOnlineWords {
			break
		}
	}
	return out
}

// fromThesaurus: статья слова -> её гиперонимы и синонимы -> словарь; если не нашли,
// ещё один шаг через гиперонимы гиперонимов («латте» -> «напиток» -> еда).
func (c *Categorizer) fromThesaurus(ctx context.Context, word string) (Guess, bool) {
	if c.thes == nil || !c.breaker.allow(c.now()) {
		return Guess{}, false
	}
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	default:
		return Guess{}, false // онлайн-поиск занят: не ждём, решит LLM
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cands := lemmaCandidates(word)
	pages, err := c.thes.Lookup(ctx, cands)
	if err != nil {
		c.breaker.fail(c.now())
		slog.WarnContext(ctx, "thesaurus lookup failed", "err", err)
		return Guess{}, false
	}
	c.breaker.success()

	lemma, related := "", []string(nil)
	for _, cand := range cands {
		if rel, ok := pages[cand]; ok {
			lemma, related = cand, rel
			break
		}
	}
	// Название - начальная форма найденного слова («пельмень»), категория - от гиперонима.
	named := func(g Guess) Guess {
		if lemma != "" {
			g.Term.Title = lemma
		}
		return g
	}
	if g, ok := c.matchRelated(related); ok {
		g = named(g)
		c.save(ctx, word, g.Term)
		return g, true
	}

	// Второй шаг: незнакомые связанные слова одним запросом.
	var next []string
	for _, r := range related {
		if _, known := c.dict.LookupWord(r); !known {
			next = append(next, r)
			if len(next) == maxHypernymHops {
				break
			}
		}
	}
	if len(next) > 0 {
		pages, err := c.thes.Lookup(ctx, next)
		if err != nil {
			c.breaker.fail(c.now())
			slog.WarnContext(ctx, "thesaurus lookup failed", "err", err)
			return Guess{}, false
		}
		for _, n := range next {
			if g, ok := c.matchRelated(pages[n]); ok {
				g = named(g)
				c.save(ctx, word, g.Term)
				return g, true
			}
		}
	}
	// Слово проверено и не распознано: отрицательный кэш, чтобы не ходить в сеть повторно.
	if lemma == "" {
		lemma = word
	}
	c.save(ctx, word, Term{Title: lemma})
	return Guess{}, false
}

func (c *Categorizer) matchRelated(related []string) (Guess, bool) {
	for _, r := range related {
		if t, ok := c.dict.LookupWord(r); ok {
			return Guess{Term: t, Source: SourceOnline}, true
		}
	}
	return Guess{}, false
}

func (c *Categorizer) save(ctx context.Context, word string, t Term) {
	if c.store == nil {
		return
	}
	if err := c.store.SaveLearnedTerm(ctx, domain.LearnedTerm{
		Stem: Stem(word), Word: word, Title: t.Title, Category: t.Category, Source: "wiktionary",
	}); err != nil {
		slog.WarnContext(ctx, "save learned term failed", "err", err)
	}
}

// breaker отключает онлайн-поиск на cooldown после failures ошибок подряд.
type breaker struct {
	mu        sync.Mutex
	failures  int
	cooldown  time.Duration
	count     int
	openUntil time.Time
}

func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !now.Before(b.openUntil)
}

func (b *breaker) fail(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count++
	if b.count >= b.failures {
		b.openUntil = now.Add(b.cooldown)
		b.count = 0
	}
}

func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count = 0
}
