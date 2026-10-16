package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ctxKey int

const requestIDKey ctxKey = iota

// requestLog присваивает запросу correlation id и пишет строку лога по завершении.
// Query string не логируется: в нём могут быть секреты.
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		id := hex.EncodeToString(b)
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r.WithContext(ctx))

		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		}
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			level = slog.LevelDebug
		}
		slog.Log(ctx, level, "http request", "request_id", id, "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(start).String())
	})
}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.ErrorContext(r.Context(), "panic in handler", "panic", v, "path", r.URL.Path, "request_id", requestID(r.Context()))
				writeAPIError(w, http.StatusInternalServerError, "internal", "Внутренняя ошибка, попробуйте позже")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders: ответы API не кэшируются и не интерпретируются браузером как другой тип.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimiter - фиксированное окно на IP клиента. За nginx все запросы приходят с одного
// адреса, и лимит становится общим; для редкого обмена токена входа это приемлемо.
type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	start   time.Time
	counter map[string]int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, counter: map[string]int{}}
}

func (l *rateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.start) >= l.window {
		l.start, l.counter = now, map[string]int{}
	}
	l.counter[key]++
	return l.counter[key] <= l.limit
}

func (s *Server) limited(l *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !l.allow(ip, time.Now()) {
			w.Header().Set("Retry-After", "60")
			writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Слишком много запросов, подождите минуту")
			return
		}
		next.ServeHTTP(w, r)
	})
}
