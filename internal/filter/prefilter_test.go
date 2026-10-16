package filter

import "testing"

func TestLooksLikeExpense(t *testing.T) {
	cases := map[string]bool{
		"Кофе 350":   true,
		"шаурма 560": true,
		"Поход в рестик 7698 вчера":    true,
		"какая ты модель?":             false,
		"сделай мне домашку по физике": false,
	}
	for in, want := range cases {
		if got := LooksLikeExpense(in); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}
