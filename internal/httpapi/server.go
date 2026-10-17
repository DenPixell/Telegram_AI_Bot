// Package httpapi - REST API для веб-панели: DTO, валидация, сессии, CSRF, маппинг ошибок в коды.
// user_id и family_id берутся только из сессии, никогда из тела запроса.
package httpapi

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"expense-bot/internal/config"
	"expense-bot/internal/metrics"
	"expense-bot/internal/service"
	"expense-bot/internal/storage/postgres"
)

// Pinger - проверка готовности БД для /readyz.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Deps struct {
	DB       Pinger
	Store    *postgres.Store
	Families *service.Families
	Expenses *service.Expenses
	Auth     *service.Auth

	AuthMode      string // config.PanelAuthMagicLink | config.PanelAuthNone
	PanelFamilyID int64  // для режима none, 0 = единственная семья
	PublicURL     string
	// BotUsername нужен для ссылок-приглашений; до запуска бота может вернуть "".
	BotUsername func() string
	// ExpandTerms расширяет поисковый запрос синонимами из словаря; nil - без расширения.
	ExpandTerms func(query string) []string

	// WebDir - собранная панель; если задан, сервер раздаёт её сам (один контейнер).
	WebDir string
	// TelegramWebhook - приём апдейтов в режиме TELEGRAM_MODE=webhook; nil - не нужен.
	TelegramWebhook http.Handler
	// MetricsToken - Bearer-токен для /metrics; пусто - без токена (за nginx) или /metrics
	// выключен, если сервер раздаёт панель сам и смотрит в интернет.
	MetricsToken string
}

type Server struct {
	Deps
	publicOrigin string
	secureCookie bool
	authLimiter  *rateLimiter
}

func New(d Deps) *Server {
	s := &Server{Deps: d, authLimiter: newRateLimiter(30, time.Minute)}
	if u, err := url.Parse(d.PublicURL); err == nil {
		s.publicOrigin = u.Scheme + "://" + u.Host
		s.secureCookie = u.Scheme == "https"
	}
	if s.BotUsername == nil {
		s.BotUsername = func() string { return "" }
	}
	if s.ExpandTerms == nil {
		s.ExpandTerms = func(q string) []string { return []string{q} }
	}
	return s
}

// Handler собирает маршруты. Ошибка - только если WebDir задан, но панели там нет.
func (s *Server) Handler() (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	// Метрики Prometheus. В compose наружу не публикуются: nginx проксирует только /api/,
	// а порт приложения слушает 127.0.0.1. В режиме одного контейнера - только с токеном.
	switch {
	case s.MetricsToken != "":
		mux.Handle("GET /metrics", bearer(s.MetricsToken, metrics.Handler()))
	case s.WebDir == "":
		mux.Handle("GET /metrics", metrics.Handler())
	}

	if s.AuthMode == config.PanelAuthMagicLink {
		mux.Handle("POST /api/auth/exchange", s.limited(s.authLimiter, http.HandlerFunc(s.exchange)))
		mux.Handle("POST /api/auth/logout", s.authed(s.logout))
	}
	mux.Handle("GET /api/me", s.authed(s.me))
	mux.Handle("GET /api/categories", s.authed(s.categories))

	mux.Handle("GET /api/expenses", s.authed(s.listExpenses))
	mux.Handle("GET /api/expenses/summary", s.authed(s.expenseSummary))
	mux.Handle("POST /api/expenses", s.authed(s.createExpense))
	mux.Handle("GET /api/expenses/{id}", s.authed(s.getExpense))
	mux.Handle("PUT /api/expenses/{id}", s.authed(s.updateExpense))
	mux.Handle("DELETE /api/expenses/{id}", s.authed(s.deleteExpense))

	mux.Handle("GET /api/family", s.authed(s.getFamily))
	mux.Handle("PUT /api/family", s.authed(s.updateFamily))

	mux.Handle("GET /api/members", s.authed(s.listMembers))
	mux.Handle("PATCH /api/members/{userID}", s.authed(s.updateMember))
	mux.Handle("DELETE /api/members/{userID}", s.authed(s.removeMember))
	mux.Handle("POST /api/members/{userID}/transfer-ownership", s.authed(s.transferOwnership))

	mux.Handle("GET /api/invites", s.authed(s.listInvites))
	mux.Handle("POST /api/invites", s.authed(s.createInvite))
	mux.Handle("DELETE /api/invites/{id}", s.authed(s.revokeInvite))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, http.StatusNotFound, "not_found", "Такого метода API нет")
	})
	if s.TelegramWebhook != nil {
		mux.Handle("POST /telegram/webhook", s.TelegramWebhook)
	}
	if s.WebDir != "" {
		site, err := newStaticSite(s.WebDir)
		if err != nil {
			return nil, err
		}
		mux.Handle("/", site)
	}
	return requestLog(mux, recoverer(securityHeaders(mux))), nil
}

func bearer(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MustHandler - для тестов: панель не раздаётся, ошибок быть не может.
func (s *Server) MustHandler() http.Handler {
	h, err := s.Handler()
	if err != nil {
		panic(err)
	}
	return h
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.DB.Ping(ctx); err != nil {
		slog.WarnContext(ctx, "readiness check failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
