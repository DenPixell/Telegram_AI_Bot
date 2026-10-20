package lexicon

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"expense-bot/internal/nlp/textnorm"

	"go.yaml.in/yaml/v3"

	"expense-bot/internal/domain"
)

// Term - каноническое название траты и его категория.
type Term struct {
	Title    string
	Category domain.Category
}

// ActionKind - вид действия над тратами, найденный по лексикону.
type ActionKind string

const (
	ActionList   ActionKind = "list"
	ActionDelete ActionKind = "delete"
	ActionUpdate ActionKind = "update"
)

// Dictionary - словарь синонимов, лексикон действий, стоп-слова и служебные слова.
// Все сравнения по основам слов (Stem), поэтому словоформы не нужно перечислять.
type Dictionary struct {
	phrases     map[string]Term // основа фразы -> термин
	maxPhrase   int             // длина самой длинной фразы в словах
	forms       map[string][]string
	actions     map[string]ActionKind // основа -> действие
	hardStop    map[string]bool
	softStop    map[string]bool
	stopPhrases []string // нормализованные фразы, ищутся как последовательность слов
	fillers     map[string]bool
}

type dictionaryFile struct {
	Terms map[string]struct {
		Category string   `yaml:"category"`
		Aliases  []string `yaml:"aliases"`
	} `yaml:"terms"`
	ActionWords map[ActionKind][]string `yaml:"action_words"`
	StopWords   struct {
		Hard    []string `yaml:"hard"`
		Soft    []string `yaml:"soft"`
		Phrases []string `yaml:"phrases"`
	} `yaml:"stop_words"`
	FillerWords []string `yaml:"filler_words"`
}

// LoadDictionary читает и проверяет словарь. Ошибка, если одна и та же форма (после стемминга)
// указывает на разные категории: такие конфликты надо решить в файле.
func LoadDictionary(path string) (*Dictionary, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: путь из конфига (SYNONYMS_PATH), не от пользователя
	if err != nil {
		return nil, fmt.Errorf("read dictionary: %w", err)
	}
	return ParseDictionary(b)
}

func ParseDictionary(b []byte) (*Dictionary, error) {
	var f dictionaryFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse dictionary: %w", err)
	}
	d := &Dictionary{
		phrases:  map[string]Term{},
		forms:    map[string][]string{},
		actions:  map[string]ActionKind{},
		hardStop: map[string]bool{},
		softStop: map[string]bool{},
		fillers:  map[string]bool{},
	}
	var errs []error

	titles := make([]string, 0, len(f.Terms))
	for t := range f.Terms {
		titles = append(titles, t)
	}
	sort.Strings(titles) // детерминированный порядок для сообщений об ошибках
	for _, title := range titles {
		def := f.Terms[title]
		cat := domain.Category(def.Category)
		if !cat.Valid() {
			errs = append(errs, fmt.Errorf("term %q: unknown category %q", title, def.Category))
			continue
		}
		// Title для показа - как в файле (с «ё»), ключи поиска - нормализованные.
		term := Term{Title: strings.ToLower(strings.TrimSpace(title)), Category: cat}
		for _, form := range append([]string{title}, def.Aliases...) {
			words := textnorm.Words(textnorm.Normalize(form))
			if len(words) == 0 {
				errs = append(errs, fmt.Errorf("term %q: alias %q has no words", title, form))
				continue
			}
			key := textnorm.StemPhrase(words)
			if prev, ok := d.phrases[key]; ok && prev.Category != term.Category {
				errs = append(errs, fmt.Errorf("alias %q of %q (%s) conflicts with %q (%s)",
					form, title, term.Category, prev.Title, prev.Category))
				continue
			} else if !ok {
				d.phrases[key] = term
			}
			d.forms[term.Title] = append(d.forms[term.Title], strings.Join(words, " "))
			d.maxPhrase = max(d.maxPhrase, len(words))
		}
	}
	for kind, words := range f.ActionWords {
		if kind != ActionList && kind != ActionDelete && kind != ActionUpdate {
			errs = append(errs, fmt.Errorf("unknown action kind %q", kind))
			continue
		}
		for _, w := range words {
			d.actions[textnorm.Stem(textnorm.Normalize(w))] = kind
		}
	}
	for _, w := range f.StopWords.Hard {
		d.hardStop[textnorm.Stem(textnorm.Normalize(w))] = true
	}
	for _, w := range f.StopWords.Soft {
		d.softStop[textnorm.Stem(textnorm.Normalize(w))] = true
	}
	for _, p := range f.StopWords.Phrases {
		d.stopPhrases = append(d.stopPhrases, strings.Join(textnorm.Words(textnorm.Normalize(p)), " "))
	}
	for _, w := range f.FillerWords {
		d.fillers[textnorm.Normalize(w)] = true
	}
	if len(d.phrases) == 0 {
		errs = append(errs, errors.New("dictionary has no terms"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("invalid dictionary: %w", err)
	}
	return d, nil
}

// Size - число форм в словаре (для логов).
func (d *Dictionary) Size() int { return len(d.phrases) }

// Match - термин, найденный в последовательности слов [Start, End).
type Match struct {
	Term       Term
	Start, End int
}

// Lookup ищет термины жадно: на каждой позиции самая длинная фраза из словаря.
func (d *Dictionary) Lookup(words []string) []Match {
	stems := make([]string, len(words))
	for i, w := range words {
		stems[i] = textnorm.Stem(w)
	}
	var res []Match
	for i := 0; i < len(words); {
		found := false
		for n := min(d.maxPhrase, len(words)-i); n >= 1; n-- {
			// Служебное слово само по себе не термин: основа «купили» совпадает с «купе».
			if n == 1 && d.fillers[words[i]] {
				continue
			}
			if t, ok := d.phrases[strings.Join(stems[i:i+n], " ")]; ok {
				res = append(res, Match{Term: t, Start: i, End: i + n})
				i += n
				found = true
				break
			}
		}
		if !found {
			// Стеммер Портера бывает несимметричен: «ресторан» -> «рестора», но «ресторане» ->
			// «ресторан». Повторный стемминг основы сводит формы к одному ключу.
			// Уменьшительные формы: «мандаринчики» -> «мандарин», «кофеечек» -> «кофе».
			if !d.fillers[words[i]] {
				for _, v := range append([]string{textnorm.Stem(stems[i])}, diminutiveStems(stems[i])...) {
					if t, ok := d.phrases[v]; ok {
						res = append(res, Match{Term: t, Start: i, End: i + 1})
						break
					}
				}
			}
			i++ // позиция сдвигается всегда, иначе служебное слово зациклит поиск
		}
	}
	return res
}

// Уменьшительно-ласкательные суффиксы основ, от длинных к коротким.
var diminutiveSuffixes = []string{"очек", "ечек", "ичк", "очк", "ечк", "ушк", "юшк", "еньк", "оньк", "чик", "щик", "ик", "ек", "ок", "к"}

// diminutiveStems - основы без уменьшительного суффикса. Основа короче 3 букв не возвращается.
func diminutiveStems(stem string) []string {
	var out []string
	for _, suf := range diminutiveSuffixes {
		if base, ok := strings.CutSuffix(stem, suf); ok && len([]rune(base)) >= 3 {
			out = append(out, base, textnorm.Stem(base+"а"), textnorm.Stem(base+"о"))
		}
	}
	return out
}

// LookupWord - термин для одного слова или фразы (для онлайн-тезауруса и кэша).
func (d *Dictionary) LookupWord(word string) (Term, bool) {
	words := textnorm.Words(textnorm.Normalize(word))
	if t, ok := d.phrases[textnorm.StemPhrase(words)]; ok {
		return t, true
	}
	if m := d.Lookup(words); len(m) == 1 && m[0].End-m[0].Start == len(words) {
		return m[0].Term, true
	}
	return Term{}, false
}

// Expand возвращает все формы терминов, найденных в запросе, для поиска в панели и боте:
// «рестик» -> [ресторан, рестик, ресторанчик, ...]. Если терминов нет - сам запрос.
func (d *Dictionary) Expand(query string) []string {
	q := textnorm.Normalize(query)
	matches := d.Lookup(textnorm.Words(q))
	if len(matches) == 0 {
		return []string{q}
	}
	seen := map[string]bool{}
	out := []string{q}
	seen[q] = true
	for _, m := range matches {
		// Заголовок как хранится в тратах (с «ё»): формы нормализованы и его не покрывают.
		if !seen[m.Term.Title] {
			seen[m.Term.Title] = true
			out = append(out, m.Term.Title)
		}
		for _, f := range d.forms[m.Term.Title] {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// Правила фильтра из того же файла словаря. Все проверки принимают основу слова (textnorm.Stem),
// кроме IsFiller и HasStopPhrase, которым нужны нормализованные слова.

// IsHardStop - запрещённая тема: сообщение отклоняется всегда.
func (d *Dictionary) IsHardStop(stem string) bool { return d.hardStop[stem] }

// IsSoftStop - подозрительное слово: отклоняется, только если в сообщении нет действия
// и известной траты.
func (d *Dictionary) IsSoftStop(stem string) bool { return d.softStop[stem] }

// Action - действие над тратами, которое обозначает слово («покажи», «удали»).
func (d *Dictionary) Action(stem string) (ActionKind, bool) {
	k, ok := d.actions[stem]
	return k, ok
}

// IsFiller - служебное слово («в», «на», «поход»): не описывает трату само по себе.
func (d *Dictionary) IsFiller(word string) bool { return d.fillers[word] }

// HasStopPhrase - в словах есть запрещённая фраза целиком.
func (d *Dictionary) HasStopPhrase(words []string) bool {
	joined := " " + strings.Join(words, " ") + " "
	for _, p := range d.stopPhrases {
		if strings.Contains(joined, " "+p+" ") {
			return true
		}
	}
	return false
}
