package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"expense-bot/internal/domain"
)

func TestParseAmounts(t *testing.T) {
	cases := []struct {
		in   string
		want []domain.Money
	}{
		{"кофе 350", []domain.Money{rub(350)}},
		{"350 кофе", []domain.Money{rub(350)}},
		{"1 200 р на аптеку", []domain.Money{rub(1200)}},
		{"1200р", []domain.Money{rub(1200)}},
		{"1200 руб", []domain.Money{rub(1200)}},
		{"1200₽", []domain.Money{rub(1200)}},
		{"1.5к бензин", []domain.Money{rub(1500)}},
		{"1,5к бензин", []domain.Money{rub(1500)}},
		{"2 тыс продукты", []domain.Money{rub(2000)}},
		{"3 косаря", []domain.Money{rub(3000)}},
		{"ремонт 1.2 млн", []domain.Money{rub(1_200_000)}},
		{"10 000 000 квартира", []domain.Money{rub(10_000_000)}},
		{"кофе 350.50", []domain.Money{35050}},
		{"кофе 350,5", []domain.Money{35050}},
		{"1.200 метро", []domain.Money{rub(1200)}}, // три цифры после точки - разряд
		{"кофе 350 и обед 600", []domain.Money{rub(350), rub(600)}},
		{"последнее было не 350, а 450", []domain.Money{rub(350), rub(450)}},
		{"Поход в рестик 7698 вчера", []domain.Money{rub(7698)}},
		// Даты и время - не суммы.
		{"кофе 12.10", nil},
		{"кофе 350 12.10", []domain.Money{rub(350)}},
		{"такси 700 01.05.2025", []domain.Money{rub(700)}},
		{"кофе 350 5 марта", []domain.Money{rub(350)}},
		{"встреча в 18:30", nil},
		// Дата с валютой - всё-таки сумма: «12.10 руб».
		{"12.10 руб", []domain.Money{1210}},
		// Ноль и мусор отбрасываются.
		{"кофе 0", nil},
		{"кофе", nil},
		{"", nil},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			var got []domain.Money
			for _, a := range parseAmounts(tokenize(Normalize(c.in))) {
				got = append(got, a.Value)
			}
			assert.Equal(t, c.want, got)
		})
	}
}

func TestParseAmountsLargeFlag(t *testing.T) {
	a := parseAmounts(tokenize(Normalize("ремонт 15000000")))
	if assert.Len(t, a, 1) {
		assert.True(t, a[0].Large, "выше 10 млн нужна подтверждение")
	}
	a = parseAmounts(tokenize(Normalize("ремонт 9000000")))
	if assert.Len(t, a, 1) {
		assert.False(t, a[0].Large)
	}
	// Больше 1 млрд (CHECK в БД) не считается суммой вовсе.
	assert.Empty(t, parseAmounts(tokenize(Normalize("2000000000 рублей"))))
}

func FuzzParseAmounts(f *testing.F) {
	for _, s := range []string{"кофе 350", "1 200 р", "1.5к", "12.10", "18:30", "5 марта", "999999999999999999 млн", "1,2,3.4.5"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, a := range parseAmounts(tokenize(Normalize(s))) {
			if !a.Value.Valid() {
				t.Fatalf("invalid amount %d from %q", a.Value, s)
			}
		}
	})
}
