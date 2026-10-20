package parse

import "expense-bot/internal/domain"

// today - пятница 16.10.2026.
var today = date("2026-10-16")

func date(s string) domain.Date {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func rub(r int64) domain.Money { return domain.Money(r * 100) }
