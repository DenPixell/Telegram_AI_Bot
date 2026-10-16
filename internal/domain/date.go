package domain

import (
	"fmt"
	"strconv"
	"time"
)

const dateLayout = "2006-01-02"

// Date - календарная дата без времени и пояса (expenses.spent_at).
// Не переводится между поясами и не «прыгает».
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf возвращает календарную дату момента t в поясе loc.
func DateOf(t time.Time, loc *time.Location) Date {
	y, m, d := t.In(loc).Date()
	return Date{y, m, d}
}

func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q: %w", s, err)
	}
	return DateOf(t, time.UTC), nil
}

// Time возвращает полночь этой даты в UTC (для хранения в колонке DATE).
func (d Date) Time() time.Time { return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC) }

func (d Date) AddDays(n int) Date { return DateOf(d.Time().AddDate(0, 0, n), time.UTC) }

func (d Date) Before(o Date) bool { return d.Time().Before(o.Time()) }

func (d Date) IsZero() bool { return d == Date{} }

func (d Date) String() string { return d.Time().Format(dateLayout) }

func (d Date) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(d.String())), nil }

func (d *Date) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return fmt.Errorf("date must be a string like \"2026-10-16\": %w", err)
	}
	v, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}
