package pipeline

import (
	"strconv"
	"strings"

	"expense-bot/internal/domain"
)

// LargeAmount - выше этого порога трата требует подтверждения (10 млн рублей).
const LargeAmount domain.Money = 10_000_000_00

// Amount - сумма, найденная в тексте.
type Amount struct {
	Value domain.Money
	Large bool // выше LargeAmount, нужно подтверждение
	// first, last - индексы токенов суммы вместе с множителем и валютой.
	first, last int
}

var multipliers = map[string]int64{
	"к": 1000, "k": 1000, "тыс": 1000, "тысяча": 1000, "тысячи": 1000, "тысяч": 1000,
	"тыщ": 1000, "тыщи": 1000, "тыща": 1000, "косарь": 1000, "косаря": 1000, "косарей": 1000,
	"млн": 1_000_000, "миллион": 1_000_000, "миллиона": 1_000_000, "миллионов": 1_000_000,
	"лям": 1_000_000, "ляма": 1_000_000, "лямов": 1_000_000,
}

var currencies = map[string]bool{
	"р": true, "руб": true, "рубль": true, "рубля": true, "рублей": true, "rub": true, "₽": true,
}

// parseAmounts находит суммы: «350», «1 200», «1200р», «1200 руб», «1200₽», «1.5к», «1,5к»,
// «2 тыс», «350.50». Даты («12.10», «12.10.2026») и время («18:30») суммами не считаются,
// если за ними не идёт валюта.
func parseAmounts(toks []token) []Amount {
	var res []Amount
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind != tokNumber {
			continue
		}
		first := i
		num := t.text
		if _, _, ok := dayOfMonth(toks, i); ok {
			i++ // «5 марта» - дата, не сумма
			continue
		}
		// Разряды через пробел: «1 200», «10 000 000». Первая группа до трёх цифр, дальше по три.
		if len(num) <= 3 && isDigits(num) {
			for i+1 < len(toks) && toks[i+1].kind == tokNumber && len(toks[i+1].text) == 3 && isDigits(toks[i+1].text) {
				num += toks[i+1].text
				i++
			}
		}

		mult, hasCurrency := int64(1), false
		j := i + 1
		if j < len(toks) && toks[j].kind == tokWord {
			if m, ok := multipliers[toks[j].text]; ok {
				mult = m
				j++
			}
		}
		if j < len(toks) && currencies[toks[j].text] {
			hasCurrency = true
			j++
		}

		if !hasCurrency && mult == 1 && (isDate(num) || strings.Contains(num, ":")) {
			continue
		}
		v, ok := parseNumber(num)
		if !ok {
			continue
		}
		total := v * mult
		if total <= 0 || total > int64(domain.MaxMoney) {
			continue
		}
		m := domain.Money(total)
		res = append(res, Amount{Value: m, Large: m > LargeAmount, first: first, last: j - 1})
		i = j - 1
	}
	return res
}

// parseNumber переводит «1200», «350.5», «1,5», «1.200» (разряд) в копейки.
func parseNumber(s string) (int64, bool) {
	if strings.Contains(s, ":") {
		return 0, false
	}
	sep := strings.IndexAny(s, ".,")
	whole, frac := s, ""
	if sep >= 0 {
		whole, frac = s[:sep], s[sep+1:]
		if strings.ContainsAny(frac, ".,") {
			// «1.200.000» - разделители разрядов.
			whole, frac = strings.NewReplacer(".", "", ",", "").Replace(s), ""
		} else if len(frac) == 3 {
			// «1.200» - разряд, а не дробь.
			whole, frac = whole+frac, ""
		}
	}
	if !isDigits(whole) || (frac != "" && (!isDigits(frac) || len(frac) > 2)) || len(whole) > 12 {
		return 0, false
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	var f int64
	if frac != "" {
		if len(frac) == 1 {
			frac += "0"
		}
		f, _ = strconv.ParseInt(frac, 10, 64)
	}
	return w*100 + f, true
}

// isDate: «12.10», «1.9», «12.10.2026», «12.10.26» с правдоподобными днём и месяцем.
func isDate(s string) bool {
	parts := strings.Split(strings.ReplaceAll(s, ",", "."), ".")
	if len(parts) < 2 || len(parts) > 3 || len(parts[0]) > 2 || len(parts[1]) > 2 {
		return false
	}
	d, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || d < 1 || d > 31 || m < 1 || m > 12 {
		return false
	}
	if len(parts) == 3 && len(parts[2]) != 2 && len(parts[2]) != 4 {
		return false
	}
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ParseAmounts возвращает все суммы в тексте. Агент сверяет с ними суммы из ответа модели:
// сумма, которой нет в сообщении, - выдумка модели.
func ParseAmounts(text string) []domain.Money {
	var out []domain.Money
	for _, a := range parseAmounts(tokenize(Normalize(text))) {
		out = append(out, a.Value)
	}
	return out
}
