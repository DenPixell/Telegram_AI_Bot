package pipeline

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"expense-bot/internal/domain"
)

var relativeDays = map[string]int{"сегодня": 0, "вчера": -1, "позавчера": -2}

// weekdays - формы дней недели после предлогов «в»/«во»/«на»: «в пятницу», «во вторник».
var weekdays = map[string]time.Weekday{
	"понедельник": time.Monday, "вторник": time.Tuesday, "среда": time.Wednesday, "среду": time.Wednesday,
	"четверг": time.Thursday, "пятница": time.Friday, "пятницу": time.Friday,
	"суббота": time.Saturday, "субботу": time.Saturday, "воскресенье": time.Sunday,
}

// months - родительный падеж: «5 марта», «1 мая».
var months = map[string]time.Month{
	"января": time.January, "февраля": time.February, "марта": time.March, "апреля": time.April,
	"мая": time.May, "июня": time.June, "июля": time.July, "августа": time.August,
	"сентября": time.September, "октября": time.October, "ноября": time.November, "декабря": time.December,
}

// dayOfMonth: токен i - число 1..31, за ним название месяца («5 марта»).
func dayOfMonth(toks []token, i int) (int, time.Month, bool) {
	if i+1 >= len(toks) || toks[i].kind != tokNumber || !isDigits(toks[i].text) {
		return 0, 0, false
	}
	m, ok := months[toks[i+1].text]
	d, err := strconv.Atoi(toks[i].text)
	if !ok || err != nil || d < 1 || d > 31 {
		return 0, 0, false
	}
	return d, m, true
}

// findDate ищет указание даты и возвращает дату относительно today и индекс токена.
// День недели - ближайший прошедший (или сегодняшний). Дата без года - текущий год, а если
// она получается позже завтрашнего дня - прошлый.
// Токены сумм (skip) датой не считаются: «1.5к» - это не 1 мая.
func findDate(toks []token, today domain.Date, skip []Amount) (domain.Date, int, bool) {
	for i, t := range toks {
		if inAmount(i, skip) {
			continue
		}
		switch t.kind {
		case tokWord:
			if off, ok := relativeDays[t.text]; ok {
				return today.AddDays(off), i, true
			}
			if wd, ok := weekdays[t.text]; ok {
				back := (int(today.Time().Weekday()) - int(wd) + 7) % 7
				return today.AddDays(-back), i, true
			}
		case tokNumber:
			if day, month, ok := dayOfMonth(toks, i); ok {
				if d, ok := parseDayMonth(fmt.Sprintf("%d.%d", day, month), today); ok {
					return d, i, true
				}
			}
			if d, ok := parseDayMonth(t.text, today); ok {
				return d, i, true
			}
		}
	}
	return domain.Date{}, -1, false
}

func parseDayMonth(s string, today domain.Date) (domain.Date, bool) {
	if !isDate(s) {
		return domain.Date{}, false
	}
	parts := strings.Split(strings.ReplaceAll(s, ",", "."), ".")
	d, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	y := today.Year
	explicitYear := len(parts) == 3
	if explicitYear {
		y, _ = strconv.Atoi(parts[2])
		if y < 100 {
			y += 2000
		}
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d { // 31.02 и подобное
		return domain.Date{}, false
	}
	res := domain.DateOf(t, time.UTC)
	if !explicitYear && today.AddDays(1).Before(res) {
		res = domain.Date{Year: y - 1, Month: res.Month, Day: res.Day}
	}
	return res, true
}

func inAmount(i int, amounts []Amount) bool {
	for _, a := range amounts {
		if i >= a.first && i <= a.last {
			return true
		}
	}
	return false
}

// FindDate - указание даты в тексте («вчера», «в пятницу», «12.10») относительно today.
// Агент подставляет её, если модель не передала дату сама.
func FindDate(text string, today domain.Date) (domain.Date, bool) {
	toks := tokenize(Normalize(text))
	d, _, ok := findDate(toks, today, parseAmounts(toks))
	return d, ok
}
