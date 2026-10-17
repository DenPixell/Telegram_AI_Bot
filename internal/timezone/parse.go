package timezone

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Parse распознаёт пояс из текста пользователя в порядке: смещение («МСК+2», «UTC+5»),
// город из встроенной таблицы («Вологда», «в Новосибирске»), IANA-имя («Asia/Omsk»).
// Служебные префиксы («мой часовой пояс», «я живу в») отбрасываются.
func Parse(text string) (Zone, error) {
	s := stripPrefixes(normalize(text))
	if s == "" {
		return Zone{}, ErrUnknown
	}
	if z, ok, err := parseOffset(s); ok {
		return z, err
	}
	if i, ok := findCity(s); ok {
		c := cities[i]
		return zoneFor(c.zone, displayName(c.names[0]))
	}
	return parseIANA(strings.TrimSpace(text))
}

// normalize: нижний регистр, ё -> е, разные тире и минусы -> "-", без обрамляющей пунктуации,
// одиночные пробелы.
func normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("ё", "е", "−", "-", "–", "-", "—", "-").Replace(s)
	s = strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(`.,!?;:"'«»()`, r)
	})
	return strings.Join(strings.Fields(s), " ")
}

// prefixes - служебные слова перед значением. Отбрасываются многократно («мой часовой пояс
// в Москве»), в порядке от длинных к коротким.
var prefixes = []string{
	"мой часовой пояс", "мой пояс", "часовой пояс", "пояс",
	"я живу в", "живу в", "я сейчас в", "сейчас в", "я в", "я из",
	"город", "г.", "г", "во", "в", "из",
}

func stripPrefixes(s string) string {
	for changed := true; changed; {
		changed = false
		for _, p := range prefixes {
			rest, ok := strings.CutPrefix(s, p)
			// Префикс - отдельное слово: после него пробел или двоеточие (или точка в «г.»).
			if !ok || rest == "" || (rest[0] != ' ' && rest[0] != ':' && !strings.HasSuffix(p, ".")) {
				continue
			}
			rest = strings.TrimLeft(rest, " :")
			if r, ok := strings.CutPrefix(rest, "- "); ok { // «пояс - мск», но не «пояс -5»
				rest = r
			}
			s, changed = rest, true
			break
		}
	}
	return s
}

var (
	cityExact = map[string]int{}
	cityStem  = map[string]int{}
)

func init() {
	for i, c := range cities {
		for _, n := range c.names {
			if _, dup := cityExact[n]; !dup {
				cityExact[n] = i
			}
			if k := stemPhrase(n); k != "" {
				if _, dup := cityStem[k]; !dup {
					cityStem[k] = i
				}
			}
		}
	}
}

// findCity ищет город точно, затем по основам слов («в Новосибирске», «из Перми»).
func findCity(s string) (int, bool) {
	if i, ok := cityExact[s]; ok {
		return i, true
	}
	if k := stemPhrase(s); k != "" {
		if i, ok := cityStem[k]; ok {
			return i, true
		}
	}
	return 0, false
}

// Окончания падежей, которые снимаются с каждого слова. Длинные проверяются первыми.
var endings = []string{"ами", "ями", "ом", "ем", "ой", "ей", "ий", "ый", "ах", "ях", "е", "у", "ю", "а", "я", "и", "ы", "ь"}

// stemPhrase снимает одно окончание с каждого слова и каждой части через дефис
// («Ростове-на-Дону» -> «ростов-на-дон»). Короткие части не трогаются, чтобы не склеивать
// разные названия.
func stemPhrase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		parts := strings.Split(w, "-")
		for j, p := range parts {
			parts[j] = stemWord(p)
		}
		words[i] = strings.Join(parts, "-")
	}
	return strings.Join(words, " ")
}

func stemWord(w string) string {
	if utf8.RuneCountInString(w) < 4 {
		return w
	}
	for _, e := range endings {
		if stem, ok := strings.CutSuffix(w, e); ok && utf8.RuneCountInString(stem) >= 3 {
			return stem
		}
	}
	return w
}

// displayName делает заглавными первые буквы слов и частей через дефис, кроме служебного
// «на»: «Ростов-на-Дону», «Йошкар-Ола».
func displayName(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		parts := strings.Split(w, "-")
		for j, p := range parts {
			if p != "на" {
				parts[j] = upperFirst(p)
			}
		}
		words[i] = strings.Join(parts, "-")
	}
	return strings.Join(words, " ")
}

func upperFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// parseIANA принимает имя вида «Asia/Omsk» в любом регистре.
func parseIANA(s string) (Zone, error) {
	if !strings.Contains(s, "/") || strings.ContainsAny(s, " .") {
		return Zone{}, ErrUnknown
	}
	// Каноническая запись первой: на macOS файловая система не различает регистр,
	// и "america/new_york" загрузился бы под неканоническим именем.
	for _, name := range []string{titleIANA(s), s} {
		if loc, ok := loadIANA(name); ok {
			return Zone{Name: loc.String(), Label: loc.String(), Location: loc}, nil
		}
	}
	return Zone{}, ErrUnknown
}

// titleIANA: "america/new_york" -> "America/New_York".
func titleIANA(s string) string {
	b := []rune(strings.ToLower(s))
	for i := range b {
		if i == 0 || strings.ContainsRune("/_-", b[i-1]) {
			b[i] = unicode.ToUpper(b[i])
		}
	}
	return string(b)
}
