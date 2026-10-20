package thesaurus

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWiktionaryRedirectDoesNotStealExactTitle(t *testing.T) {
	// «собаки» перенаправляет на «собака»; запрошены обе формы - статья должна достаться «собака».
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "собака|собаки", r.URL.Query().Get("titles"))
		assert.Contains(t, r.Header.Get("User-Agent"), "expense-bot")
		_, _ = io.WriteString(w, `{"query":{
			"redirects":[{"from":"собаки","to":"собака"}],
			"pages":[{"title":"собака","revisions":[{"slots":{"main":{"content":"= {{-ru-}} =\n==== Гиперонимы ====\n# [[животное]]\n"}}}]}]}}`)
	}))
	defer srv.Close()

	pages, err := NewWiktionary(srv.URL, time.Second).Lookup(context.Background(), []string{"собака", "собаки"})
	require.NoError(t, err)
	require.Contains(t, pages, "собака")
	assert.Equal(t, []string{"животное"}, pages["собака"].Hypernyms)
	assert.NotContains(t, pages, "собаки")
}

func TestWiktionaryHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := NewWiktionary(srv.URL, time.Second).Lookup(context.Background(), []string{"x"})
	assert.Error(t, err)
}

func TestRelatedFromWikitext(t *testing.T) {
	text := "= {{-ru-}} =\n==== Синонимы ====\n# [[кебаб]], [[донер-кебаб|донер]]\n==== Гиперонимы ====\n# [[блюдо]]\n==== Гипонимы ====\n# [[шаверма]]\n= {{-en-}} =\n==== Синонимы ====\n# [[wrap]]"
	r := relatedFromWikitext(text)
	assert.Equal(t, []string{"блюдо"}, r.Hypernyms)
	assert.Equal(t, []string{"кебаб", "донер-кебаб"}, r.Synonyms, "только русская секция, без гипонимов")
}

func TestLemmaCandidates(t *testing.T) {
	assert.Contains(t, LemmaCandidates("шаурмой"), "шаурма")
	assert.Equal(t, "латте", LemmaCandidates("латте")[0], "само слово - первым")
}
