package pipeline

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

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
	b, err := os.ReadFile(path)
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
		term := Term{Title: Normalize(title), Category: cat}
		for _, form := range append([]string{title}, def.Aliases...) {
			words := wordsOf(Normalize(form))
			if len(words) == 0 {
				errs = append(errs, fmt.Errorf("term %q: alias %q has no words", title, form))
				continue
			}
			key := stemPhrase(words)
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
			d.actions[Stem(Normalize(w))] = kind
		}
	}
	for _, w := range f.StopWords.Hard {
		d.hardStop[Stem(Normalize(w))] = true
	}
	for _, w := range f.StopWords.Soft {
		d.softStop[Stem(Normalize(w))] = true
	}
	for _, p := range f.StopWords.Phrases {
		d.stopPhrases = append(d.stopPhrases, strings.Join(wordsOf(Normalize(p)), " "))
	}
	for _, w := range f.FillerWords {
		d.fillers[Normalize(w)] = true
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
		stems[i] = Stem(w)
	}
	var res []Match
	for i := 0; i < len(words); {
		found := false
		for n := min(d.maxPhrase, len(words)-i); n >= 1; n-- {
			if t, ok := d.phrases[strings.Join(stems[i:i+n], " ")]; ok {
				res = append(res, Match{Term: t, Start: i, End: i + n})
				i += n
				found = true
				break
			}
		}
		if !found {
			// Уменьшительные формы: «мандаринчики» -> «мандарин», «кофеечек» -> «кофе».
			for _, v := range diminutiveStems(stems[i]) {
				if t, ok := d.phrases[v]; ok {
					res = append(res, Match{Term: t, Start: i, End: i + 1})
					found = true
					break
				}
			}
			i++
		}
	}
	return res
}

// Уменьшительно-ласкательные суффиксы основ, от длинных к коротким.
var diminutiveSuffixes = []string{"очек", "ечек", "ичк", "очк", "ечк", "ушк", "юшк", "еньк", "оньк", "чик", "щик", "ик", "ек", "ок"}

// diminutiveStems - основы без уменьшительного суффикса. Основа короче 3 букв не возвращается.
func diminutiveStems(stem string) []string {
	var out []string
	for _, suf := range diminutiveSuffixes {
		if base, ok := strings.CutSuffix(stem, suf); ok && len([]rune(base)) >= 3 {
			out = append(out, base, Stem(base+"а"), Stem(base+"о"))
		}
	}
	return out
}

// LookupWord - термин для одного слова или фразы (для онлайн-тезауруса и кэша).
func (d *Dictionary) LookupWord(word string) (Term, bool) {
	t, ok := d.phrases[stemPhrase(wordsOf(Normalize(word)))]
	return t, ok
}

// Expand возвращает все формы терминов, найденных в запросе, для поиска в панели и боте:
// «рестик» -> [ресторан, рестик, ресторанчик, ...]. Если терминов нет - сам запрос.
func (d *Dictionary) Expand(query string) []string {
	q := Normalize(query)
	matches := d.Lookup(wordsOf(q))
	if len(matches) == 0 {
		return []string{q}
	}
	seen := map[string]bool{}
	out := []string{q}
	seen[q] = true
	for _, m := range matches {
		for _, f := range d.forms[m.Term.Title] {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// wordsOf - только словесные токены текста.
func wordsOf(s string) []string {
	var out []string
	for _, t := range tokenize(s) {
		if t.kind == tokWord {
			out = append(out, t.text)
		}
	}
	return out
}
