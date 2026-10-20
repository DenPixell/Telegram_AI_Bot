package textnorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		in   string
		want []Token
	}{
		{"кофе 350", []Token{{"кофе", KindWord}, {"350", KindNumber}}},
		{"1200р", []Token{{"1200", KindNumber}, {"р", KindWord}}},
		{"1.5к бензин", []Token{{"1.5", KindNumber}, {"к", KindWord}, {"бензин", KindWord}}},
		{"яндекс-такси 500₽", []Token{{"яндекс-такси", KindWord}, {"500", KindNumber}, {"₽", KindSymbol}}},
		{"12.10, 18:30!", []Token{{"12.10", KindNumber}, {"18:30", KindNumber}}},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, Tokenize(tt.in))
		})
	}
}

func TestNormalizeAndWords(t *testing.T) {
	assert.Equal(t, "еще кофе - 350", Normalize("  ЕЩЁ   Кофе — 350 "))
	assert.Equal(t, []string{"поход", "в", "рестик"}, Words(Normalize("Поход в рестик 7698")))
}

func TestStem(t *testing.T) {
	for _, w := range []string{"продукты", "продуктов", "продуктами"} {
		assert.Equal(t, "продукт", Stem(w), w)
	}
	// Известная несимметричность стеммера: словарь сводит такие формы повторным стеммингом.
	assert.Equal(t, "рестора", Stem("ресторан"))
	assert.Equal(t, "ресторан", Stem("ресторане"))
	assert.Equal(t, "coffee", Stem("coffee"), "латиница не стеммится")
	assert.Equal(t, Stem("яндекс")+"-"+Stem("такси"), Stem("яндекс-такси"))
}
