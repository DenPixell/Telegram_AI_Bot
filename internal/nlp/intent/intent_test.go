package intent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchIntent(t *testing.T) {
	cases := []struct {
		text string
		kind Kind
		arg  string
	}{
		{"помощь", Help, ""},
		{"/help", Help, ""},
		{"Панель!", Panel, ""},
		{"/panel@MyFamilyBudgetplus_Bot", Panel, ""},
		{"ссылка", Invite, ""},
		{"выйти из семьи", Leave, ""},
		{"удалить мои данные", DeleteData, ""},
		{"/tz", Timezone, ""},
		{"мой часовой пояс Новосибирск", Timezone, "Новосибирск"},
		{"/tz@bot Asia/Omsk", Timezone, "Asia/Omsk"}, // регистр аргумента сохраняется
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			in, ok := Match(c.text)
			require.True(t, ok)
			assert.Equal(t, c.kind, in.Kind)
			assert.Equal(t, c.arg, in.Arg)
		})
	}
	for _, text := range []string{"кофе 350", "пояс 500", "панелька 300", "покажи траты"} {
		_, ok := Match(text)
		assert.False(t, ok, text)
	}
}
