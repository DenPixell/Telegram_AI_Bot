package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func panelDir(t *testing.T) string {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<html><script>theme()</script><div id="root"></div></html>`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "assets"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "app-1a2b.js"), []byte(`console.log(1)`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "favicon.svg"), []byte(`<svg/>`), 0o600))
	return dir
}

func get(h http.Handler, method, path string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStaticPanel(t *testing.T) {
	h := New(Deps{DB: fakePinger{}, AuthMode: "magic_link", WebDir: panelDir(t)}).MustHandler()

	for _, p := range []string{"/", "/auth?token=abc", "/family", "/etc/passwd"} {
		rec := get(h, http.MethodGet, p)
		assert.Equal(t, http.StatusOK, rec.Code, p)
		assert.Contains(t, rec.Body.String(), `<div id="root">`, "SPA-фолбэк: %s", p)
	}

	// Выход за пределы каталога: mux нормализует путь, файлы вне панели не отдаются.
	rec := get(h, http.MethodGet, "/../../etc/passwd")
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/etc/passwd", rec.Header().Get("Location"))

	rec = get(h, http.MethodGet, "/")
	sum := sha256.Sum256([]byte("theme()"))
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'",
		"встроенный скрипт темы разрешён по хешу")
	csp := rec.Header().Get("Content-Security-Policy")
	scriptSrc := csp[strings.Index(csp, "script-src"):]
	scriptSrc = scriptSrc[:strings.Index(scriptSrc, ";")]
	assert.NotContains(t, scriptSrc, "unsafe-inline", "скрипты - только свои и по хешу")
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))

	rec = get(h, http.MethodGet, "/assets/app-1a2b.js")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable")
	assert.Equal(t, http.StatusOK, get(h, http.MethodGet, "/favicon.svg").Code)
	assert.Equal(t, http.StatusMethodNotAllowed, get(h, http.MethodPost, "/").Code)

	// API по-прежнему отвечает JSON, а не страницей панели.
	rec = get(h, http.MethodGet, "/api/unknown")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), `"not_found"`)
}

func TestStaticPanelRequiresIndex(t *testing.T) {
	_, err := New(Deps{WebDir: t.TempDir()}).Handler()
	assert.Error(t, err)
}

func TestMetricsExposure(t *testing.T) {
	// За nginx (compose): /metrics без токена.
	h := New(Deps{DB: fakePinger{}}).MustHandler()
	assert.Equal(t, http.StatusOK, get(h, http.MethodGet, "/metrics").Code)

	// Публичный сервер с панелью и без токена: /metrics не публикуется.
	h = New(Deps{DB: fakePinger{}, WebDir: panelDir(t)}).MustHandler()
	rec := get(h, http.MethodGet, "/metrics")
	assert.NotContains(t, rec.Body.String(), "# TYPE", "метрики не отдаются")

	// С токеном - только с верным Bearer.
	h = New(Deps{DB: fakePinger{}, WebDir: panelDir(t), MetricsToken: "t0ken"}).MustHandler()
	assert.Equal(t, http.StatusUnauthorized, get(h, http.MethodGet, "/metrics").Code)
	assert.Equal(t, http.StatusUnauthorized, get(h, http.MethodGet, "/metrics", "Authorization", "Bearer wrong").Code)
	assert.Equal(t, http.StatusOK, get(h, http.MethodGet, "/metrics", "Authorization", "Bearer t0ken").Code)
}
