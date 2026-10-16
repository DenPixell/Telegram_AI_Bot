// Работа с часовыми поясами: в БД всё в UTC, отображение - в поясе пользователя.
package timeutil

import "time"

func Load(name, fallback string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	if loc, err := time.LoadLocation(fallback); err == nil {
		return loc
	}
	return time.UTC
}

// DayBounds - начало и конец суток в поясе loc, переведённые в UTC (для выборок "за сегодня").
func DayBounds(t time.Time, loc *time.Location) (time.Time, time.Time) {
	lt := t.In(loc)
	start := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	return start.UTC(), start.AddDate(0, 0, 1).UTC()
}
