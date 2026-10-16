// Package httpapi - REST API для веб-панели: DTO, валидация, сессии, CSRF, маппинг ошибок в коды.
// user_id и family_id берутся только из сессии, никогда из тела запроса.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"expense-bot/internal/config"
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

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)

	if s.AuthMode == config.PanelAuthMagicLink {
		mux.Handle("POST /api/auth/exchange", s.limited(s.authLimiter, http.HandlerFunc(s.exchange)))
		mux.Handle("POST /api/auth/logout", s.authed(s.logout))
	}
	mux.Handle("GET /api/me", s.authed(s.me))
	mux.Handle("GET /api/categories", s.authed(s.categories))

	mux.Handle("GET /api/expenses", s.authed(s.listExpenses))
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
	return requestLog(recoverer(securityHeaders(mux)))
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
