package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Раздача собранной панели (web/dist) самим Go-сервером - для хостингов с одним контейнером
// (Render free). Повторяет nginx.conf: SPA-фолбэк на index.html, кэш ассетов, строгий CSP.

var inlineScriptRe = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

type staticSite struct {
	dir   string
	index []byte
	csp   string
	files http.Handler
}

func newStaticSite(dir string) (*staticSite, error) {
	index, err := os.ReadFile(filepath.Join(dir, "index.html")) //nolint:gosec // G304: WEB_DIR из конфига
	if err != nil {
		return nil, fmt.Errorf("panel: %w", err)
	}
	// Скрипты разрешены только свои и встроенный скрипт темы по хешу - без 'unsafe-inline'.
	scriptSrc := "'self'"
	for _, m := range inlineScriptRe.FindAllSubmatch(index, -1) {
		sum := sha256.Sum256(m[1])
		scriptSrc += " 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	return &staticSite{
		dir:   dir,
		index: index,
		files: http.FileServer(http.Dir(dir)),
		csp: "default-src 'self'; script-src " + scriptSrc + "; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
			"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
	}, nil
}

func (s *staticSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h := w.Header()
	h.Set("X-Frame-Options", "DENY")
	clean := path.Clean("/" + r.URL.Path)
	if strings.HasPrefix(clean, "/assets/") {
		// Имена ассетов содержат хеш содержимого - можно кэшировать навсегда.
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		s.files.ServeHTTP(w, r)
		return
	}
	if clean != "/" && clean != "/index.html" && s.isFile(clean) {
		h.Set("Cache-Control", "no-cache")
		s.files.ServeHTTP(w, r)
		return
	}
	// Любой другой путь (/, /auth и т.п.) - SPA.
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Content-Security-Policy", s.csp)
	_, _ = w.Write(s.index) //nolint:gosec // G705: собственный index.html, не пользовательский ввод
}

// isFile проверяет файл через http.Dir: он сам не выпускает путь за пределы каталога.
func (s *staticSite) isFile(name string) bool {
	f, err := http.Dir(s.dir).Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	return err == nil && !st.IsDir()
}
