// Package httpapi - REST API для веб-панели: DTO, валидация, сессии, CSRF, маппинг ошибок в коды.
// user_id и family_id берутся только из сессии, никогда из тела запроса.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Pinger - проверка готовности зависимостей (БД) для /readyz.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Server struct {
	db Pinger
}

func New(db Pinger) *Server { return &Server{db: db} }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	// TODO(этап 4): /auth, /api/expenses, /api/members, /api/invites, /api/family.
	return recoverer(mux)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		slog.WarnContext(ctx, "readiness check failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
