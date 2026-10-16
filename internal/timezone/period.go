package timezone

import (
	"fmt"
	"time"

	"expense-bot/internal/domain"
)

// Period - период выборки из инструмента list_expenses и фильтров панели.
type Period string

const (
	PeriodToday     Period = "today"
	PeriodYesterday Period = "yesterday"
	PeriodWeek      Period = "week"       // с понедельника текущей недели по сегодня
	PeriodMonth     Period = "month"      // с 1-го числа текущего месяца по сегодня
	PeriodLastMonth Period = "last_month" // весь прошлый календарный месяц
	PeriodAll       Period = "all"
	PeriodCustom    Period = "custom" // границы from/to задаются явно
)

// Periods - допустимые значения (enum в схеме инструмента).
var Periods = []Period{PeriodToday, PeriodYesterday, PeriodWeek, PeriodMonth, PeriodLastMonth, PeriodAll, PeriodCustom}

func (p Period) Valid() bool {
	for _, v := range Periods {
		if p == v {
			return true
		}
	}
	return false
}

// Today - текущая календарная дата в поясе пользователя.
func Today(now time.Time, loc *time.Location) domain.Date { return domain.DateOf(now, loc) }

// Range вычисляет границы периода включительно относительно today (даты пользователя).
// Нулевая граница означает «без ограничения». Для PeriodCustom используются from и to,
// любая из них может быть нулевой; from позже to - ошибка валидации.
func (p Period) Range(today, from, to domain.Date) (domain.Date, domain.Date, error) {
	switch p {
	case PeriodToday:
		return today, today, nil
	case PeriodYesterday:
		y := today.AddDays(-1)
		return y, y, nil
	case PeriodWeek:
		// time.Weekday: воскресенье = 0, неделя в РФ начинается с понедельника.
		offset := (int(today.Time().Weekday()) + 6) % 7
		return today.AddDays(-offset), today, nil
	case PeriodMonth:
		return domain.Date{Year: today.Year, Month: today.Month, Day: 1}, today, nil
	case PeriodLastMonth:
		firstThis := domain.Date{Year: today.Year, Month: today.Month, Day: 1}
		lastPrev := firstThis.AddDays(-1)
		return domain.Date{Year: lastPrev.Year, Month: lastPrev.Month, Day: 1}, lastPrev, nil
	case PeriodAll:
		return domain.Date{}, domain.Date{}, nil
	case PeriodCustom:
		if !from.IsZero() && !to.IsZero() && to.Before(from) {
			return domain.Date{}, domain.Date{}, fmt.Errorf("%w: начало периода %s позже конца %s", domain.ErrValidation, from, to)
		}
		return from, to, nil
	}
	return domain.Date{}, domain.Date{}, fmt.Errorf("%w: неизвестный период %q", domain.ErrValidation, p)
}
