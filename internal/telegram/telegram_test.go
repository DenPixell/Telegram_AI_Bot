package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitMessage(t *testing.T) {
	assert.Equal(t, []string{"коротко"}, splitMessage("коротко", 4096))

	lines := strings.Repeat("строка трат 123 ₽\n", 500) // ~9000 символов
	parts := splitMessage(lines, 4096)
	require.Greater(t, len(parts), 1)
	for _, p := range parts {
		assert.LessOrEqual(t, utf8.RuneCountInString(p), 4096)
		assert.True(t, strings.HasSuffix(p, "₽"), "режется по границе строки")
	}
	assert.Equal(t, strings.TrimRight(lines, "\n"), strings.Join(parts, "\n"), "ничего не теряется")

	// Одна строка длиннее лимита режется по символам, а не по байтам.
	long := strings.Repeat("я", 5000)
	parts = splitMessage(long, 4096)
	assert.Equal(t, []int{4096, 904}, []int{utf8.RuneCountInString(parts[0]), utf8.RuneCountInString(parts[1])})
}

func TestCommand(t *testing.T) {
	cases := []struct{ in, name, payload string }{
		{"/start", "start", ""},
		{"/start K7M2Q9XH4T", "start", "K7M2Q9XH4T"},
		{"/start@MyFamilyBudgetplus_Bot K7M2Q9XH4T", "start", "K7M2Q9XH4T"},
		{"/HELP", "help", ""},
	}
	for _, c := range cases {
		name, payload, ok := command(c.in)
		assert.True(t, ok)
		assert.Equal(t, c.name, name, c.in)
		assert.Equal(t, c.payload, payload, c.in)
	}
	_, _, ok := command("кофе 350")
	assert.False(t, ok)
}

func TestAPIErrorsAndTokenRedaction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasPrefix(r.URL.Path, "/botSECRET-TOKEN/"))
		_, _ = io.WriteString(w, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
	}))
	defer srv.Close()

	err := NewAPI(srv.URL, "SECRET-TOKEN").SendMessage(context.Background(), 1, "hi", nil)
	require.Error(t, err)
	assert.True(t, IsBlocked(err))

	// Сетевая ошибка содержит URL с токеном - он не должен попасть в текст ошибки и логи.
	err = NewAPI("http://127.0.0.1:9", "SECRET-TOKEN").SendMessage(context.Background(), 1, "hi", nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET-TOKEN")
}
