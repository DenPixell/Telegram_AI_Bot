package httpapi

import (
	"crypto/subtle"
	"net/http"

	"expense-bot/internal/config"
	"expense-bot/internal/domain"
	"expense-bot/internal/service"
)

const (
	sessionCookie = "sid"
	csrfHeader    = "X-CSRF-Token"
)

type actorHandler func(w http.ResponseWriter, r *http.Request, a domain.Actor)

// authed определяет владельца из сессии (magic_link) или из конфигурации (none) и для
// изменяющих запросов проверяет Origin и CSRF-токен.
func (s *Server) authed(h actorHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUnsafe(r.Method) && !s.originAllowed(r) {
			writeAPIError(w, http.StatusForbidden, "bad_origin", "Запрос с чужого сайта отклонён")
			return
		}

		var (
			a   domain.Actor
			err error
		)
		if s.AuthMode == config.PanelAuthNone {
			a, err = service.PanelOwner(r.Context(), s.Store, s.PanelFamilyID)
		} else {
			a, err = s.Auth.SessionActor(r.Context(), sessionID(r))
		}
		if err != nil {
			writeError(w, r, err)
			return
		}

		if s.AuthMode == config.PanelAuthMagicLink && isUnsafe(r.Method) {
			want := service.CSRFToken(sessionID(r))
			got := r.Header.Get(csrfHeader)
			if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
				writeAPIError(w, http.StatusForbidden, "bad_csrf", "Обновите страницу и повторите действие")
				return
			}
		}
		h(w, r, a)
	})
}

func isUnsafe(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// originAllowed: браузер присылает Origin для изменяющих запросов; если он есть, он должен
// совпадать с PUBLIC_URL. Без Origin (не браузер) решает CSRF-токен.
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == s.publicOrigin
}

func sessionID(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

type exchangeRequest struct {
	Token string `json:"token"`
}

// exchange обменивает одноразовый токен из ссылки бота на сессию. Это POST со страницы
// панели, а не GET по ссылке: превью ссылок в Telegram иначе «съедали» бы токен.
func (s *Server) exchange(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		writeAPIError(w, http.StatusForbidden, "bad_origin", "Запрос с чужого сайта отклонён")
		return
	}
	var req exchangeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sess, err := s.Auth.Exchange(r.Context(), req.Token)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Secure выставляется по схеме PUBLIC_URL: true за HTTPS (профиль prod), false для localhost.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure зависит от конфигурации
		Name:     sessionCookie,
		Value:    sess.ID,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   s.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
	s.writeMe(w, r, sess.Actor, service.CSRFToken(sess.ID))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ domain.Actor) {
	if err := s.Auth.Logout(r.Context(), sessionID(r)); err != nil {
		writeError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: удаление cookie, Secure как при выдаче
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

type meResponse struct {
	UserID    int64     `json:"user_id"`
	Role      string    `json:"role"`
	Family    familyDTO `json:"family"`
	CSRFToken string    `json:"csrf_token,omitempty"`
	AuthMode  string    `json:"auth_mode"`
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	csrf := ""
	if s.AuthMode == config.PanelAuthMagicLink {
		csrf = service.CSRFToken(sessionID(r))
	}
	s.writeMe(w, r, a, csrf)
}

func (s *Server) writeMe(w http.ResponseWriter, r *http.Request, a domain.Actor, csrf string) {
	fam, err := s.Families.GetFamily(r.Context(), a)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, meResponse{
		UserID:    a.UserID,
		Role:      string(a.Role),
		Family:    toFamilyDTO(fam),
		CSRFToken: csrf,
		AuthMode:  s.AuthMode,
	})
}
