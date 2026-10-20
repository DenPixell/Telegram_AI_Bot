package textnorm

import (
	"strings"
	"unicode"

	"github.com/kljensen/snowball/russian"
)

// Kind - вид токена.
type Kind int

const (
	KindWord   Kind = iota // буквы и дефисы: «шаурма», «яндекс-такси»
	KindNumber             // цифры с внутренними . , : — «1200», «1.5», «12.10», «18:30»
	KindSymbol             // «₽», «$»
)

// Token - слово, число или символ валюты из нормализованного текста.
type Token struct {
	Text string
	Kind Kind
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
func Tokenize(s string) []Token {
	var (
		toks []Token
		cur  []rune
		kind Kind
	)
	flush := func() {
		if len(cur) > 0 {
			t := strings.Trim(string(cur), ".,:-")
			if t != "" {
				toks = append(toks, Token{Text: t, Kind: kind})
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
			if kind != KindWord {
				flush()
				kind = KindWord
			}
			cur = append(cur, r)
		case r == '-' && kind == KindWord && len(cur) > 0 && unicode.IsLetter(next):
			cur = append(cur, r)
		case unicode.IsDigit(r):
			if kind != KindNumber {
				flush()
				kind = KindNumber
			}
			cur = append(cur, r)
		case (r == '.' || r == ',' || r == ':') && kind == KindNumber && len(cur) > 0 && unicode.IsDigit(next):
			cur = append(cur, r)
		case r == '₽' || r == '$' || r == '€':
			flush()
			toks = append(toks, Token{Text: string(r), Kind: KindSymbol})
		default:
			flush()
		}
	}
	flush()
	return toks
}

// Stem - основа слова (стеммер Портера для русского), для сравнения словоформ:
// «продукты», «продуктов», «продуктами» -> «продукт». Стеммер бывает несимметричен
// («ресторан» -> «рестора», «ресторане» -> «ресторан»); словарь это учитывает (lexicon).
func Stem(word string) string {
	if !IsCyrillic(word) {
		return word
	}
	parts := strings.Split(word, "-")
	for i, p := range parts {
		parts[i] = russian.Stem(p, true)
	}
	return strings.Join(parts, "-")
}

// stemPhrase - основы слов фразы через пробел: ключ словаря для многословных синонимов.
func StemPhrase(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = Stem(w)
	}
	return strings.Join(out, " ")
}

func IsCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// Words - только словесные токены текста.
func Words(s string) []string {
	var out []string
	for _, t := range Tokenize(s) {
		if t.Kind == KindWord {
			out = append(out, t.Text)
		}
	}
	return out
}
