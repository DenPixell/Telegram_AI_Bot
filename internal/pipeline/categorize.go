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
//  3. онлайн-тезаурус (голосование гиперонимов и синонимов из Викисловаря), результат кэшируется;
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

// fromThesaurus: статья слова -> голосование её гиперонимов и синонимов по словарю; если
// решения нет, ещё один шаг через незнакомые гиперонимы («латте» -> «напиток» -> еда).
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

	lemma, rel := "", Related{}
	for _, cand := range cands {
		if r, ok := pages[cand]; ok {
			lemma, rel = cand, r
			break
		}
	}
	if lemma == "" {
		lemma = word
	}
	found := func(cat domain.Category) (Guess, bool) {
		g := Guess{Term: Term{Title: lemma, Category: cat}, Source: SourceOnline}
		c.save(ctx, word, g.Term)
		return g, true
	}
	v := c.newVote()
	v.add(rel)
	if cat, ok := v.winner(); ok {
		return found(cat)
	}

	// Второй шаг: незнакомые гиперонимы одним запросом, их голоса добавляются к первым.
	var next []string
	for _, h := range rel.Hypernyms {
		if _, known := c.dict.LookupWord(h); !known {
			next = append(next, h)
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
			v.add(Related{Hypernyms: pages[n].Hypernyms}) // синонимы второго уровня слишком далеки
		}
		if cat, ok := v.winner(); ok {
			return found(cat)
		}
	}
	// Слово проверено и не распознано: отрицательный кэш, чтобы не ходить в сеть повторно.
	c.save(ctx, word, Term{Title: lemma})
	return Guess{}, false
}

// Голосование: гипероним, найденный в словаре, весит 2, синоним - 1. Категория принимается,
// если набрала не меньше minVotes и строго больше любой другой. Один случайный синоним
// многозначного слова не решает ничего - тогда решает LLM.
const (
	hypernymWeight = 2
	synonymWeight  = 1
	minVotes       = 2
)

type vote struct {
	dict   *Dictionary
	scores map[domain.Category]int
}

func (c *Categorizer) newVote() *vote { return &vote{dict: c.dict, scores: map[domain.Category]int{}} }

func (v *vote) add(r Related) {
	for _, w := range r.Hypernyms {
		if t, ok := v.dict.LookupWord(w); ok {
			v.scores[t.Category] += hypernymWeight
		}
	}
	for _, w := range r.Synonyms {
		if t, ok := v.dict.LookupWord(w); ok {
			v.scores[t.Category] += synonymWeight
		}
	}
}

func (v *vote) winner() (domain.Category, bool) {
	var best domain.Category
	bestScore, second := 0, 0
	for cat, s := range v.scores {
		switch {
		case s > bestScore:
			best, bestScore, second = cat, s, bestScore
		case s > second:
			second = s
		}
	}
	return best, bestScore >= minVotes && bestScore > second
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
