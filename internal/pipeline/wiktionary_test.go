package pipeline

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
		io.WriteString(w, `{"query":{
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
