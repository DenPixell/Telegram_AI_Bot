package pipeline

import (
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
