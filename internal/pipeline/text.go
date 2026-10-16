package pipeline

import (
	"strings"
	"unicode"

	"github.com/kljensen/snowball/russian"
)

type tokenKind int

const (
	tokWord   tokenKind = iota // буквы и дефисы: «шаурма», «яндекс-такси»
	tokNumber                  // цифры с внутренними . , : — «1200», «1.5», «12.10», «18:30»
	tokSymbol                  // «₽», «$»
)

type token struct {
	text string
	kind tokenKind
}

// Normalize приводит текст к виду для правил: нижний регистр, ё -> е, разные тире -> "-",
// одиночные пробелы.
func Normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("ё", "е", "−", "-", "–", "-", "—", "-", " ", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// tokenize режет нормализованный текст на слова, числа и символы валют.
// Склеенные формы разделяются по смене класса: «1200р» -> «1200», «р»; «1.5к» -> «1.5», «к».
func tokenize(s string) []token {
	var (
		toks []token
		cur  []rune
		kind tokenKind
	)
	flush := func() {
		if len(cur) > 0 {
			t := strings.Trim(string(cur), ".,:-")
			if t != "" {
				toks = append(toks, token{text: t, kind: kind})
			}
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		next := rune(0)
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		switch {
		case unicode.IsLetter(r):
			if kind != tokWord {
				flush()
				kind = tokWord
			}
			cur = append(cur, r)
		case r == '-' && kind == tokWord && len(cur) > 0 && unicode.IsLetter(next):
			cur = append(cur, r)
		case unicode.IsDigit(r):
			if kind != tokNumber {
				flush()
				kind = tokNumber
			}
			cur = append(cur, r)
		case (r == '.' || r == ',' || r == ':') && kind == tokNumber && len(cur) > 0 && unicode.IsDigit(next):
			cur = append(cur, r)
		case r == '₽' || r == '$' || r == '€':
			flush()
			toks = append(toks, token{text: string(r), kind: tokSymbol})
		default:
			flush()
		}
	}
	flush()
	return toks
}

// Stem - основа слова (стеммер Портера для русского), для сравнения словоформ:
// «ресторане», «ресторанов», «ресторан» -> «ресторан».
func Stem(word string) string {
	if !isCyrillic(word) {
		return word
	}
	parts := strings.Split(word, "-")
	for i, p := range parts {
		parts[i] = russian.Stem(p, true)
	}
	return strings.Join(parts, "-")
}

// stemPhrase - основы слов фразы через пробел: ключ словаря для многословных синонимов.
func stemPhrase(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = Stem(w)
	}
	return strings.Join(out, " ")
}

func isCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}
