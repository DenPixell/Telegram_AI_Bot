package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/config"
	"expense-bot/internal/domain"
	"expense-bot/internal/service"
	"expense-bot/internal/storage/postgres"
	"expense-bot/internal/testutil/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

const origin = "http://panel.test"

// env - API поверх настоящей БД. Семья «Ивановы»: владелец 1, участник 2. Чужая семья: владелец 3.
type env struct {
	t        *testing.T
	store    *postgres.Store
	families *service.Families
	auth     *service.Auth
	srv      *httptest.Server
	client   *http.Client
	csrf     string
	owner    domain.Actor
	foreignE int64 // трата чужой семьи
}

func newEnv(t *testing.T, mode string) *env {
	t.Helper()
	ctx := context.Background()
	s, _ := pgtest.New(t)
	fams := service.NewFamilies(s, service.FamiliesConfig{MaxMembers: 10, InviteTTL: time.Hour, InviteMaxUses: 10, DefaultTZ: "Europe/Moscow"})
	for _, id := range []int64{1, 2, 3} {
		_, err := s.UpsertUser(ctx, id, "user"+strconv.Itoa(int(id)), "")
		require.NoError(t, err)
	}
	_, inv, err := fams.CreateFamily(ctx, 1, "Ивановы")
	require.NoError(t, err)
	_, err = fams.JoinByCode(ctx, 2, inv.Code)
	require.NoError(t, err)
	_, _, err = fams.CreateFamily(ctx, 3, "Петровы")
	require.NoError(t, err)
	owner, _ := fams.Actor(ctx, 1)
	foreign, _ := fams.Actor(ctx, 3)

	exp := service.NewExpenses(s, "Europe/Moscow")
	_, err = exp.Create(ctx, owner, domain.Expense{Amount: 35000, Category: domain.CategoryFood, Title: "кофе"})
	require.NoError(t, err)
	_, err = exp.Create(ctx, owner, domain.Expense{UserID: 2, Amount: 120000, Category: domain.CategoryTransport, Title: "такси"})
	require.NoError(t, err)
	fe, err := exp.Create(ctx, foreign, domain.Expense{Amount: 99900, Category: domain.CategoryFood, Title: "ресторан"})
	require.NoError(t, err)

	auth := service.NewAuth(s)
	api := New(Deps{
		DB: s, Store: s, Families: fams, Expenses: exp, Auth: auth,
		AuthMode: mode, PanelFamilyID: owner.FamilyID, PublicURL: origin,
		BotUsername: func() string { return "test_bot" },
	})
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &env{t: t, store: s, families: fams, auth: auth, srv: srv, client: &http.Client{Jar: jar}, owner: owner, foreignE: fe.ID}
}

// do отправляет запрос как браузер панели: Origin и CSRF-токен после входа.
func (e *env) do(method, path string, body any, hdr ...string) (int, map[string]any) {
	e.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = bytes.NewBufferString(b)
	default:
		raw, _ := json.Marshal(b)
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, r)
	require.NoError(e.t, err)
	req.Header.Set("Origin", origin)
	if e.csrf != "" {
		req.Header.Set("X-CSRF-Token", e.csrf)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	resp, err := e.client.Do(req)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (e *env) login() {
	e.t.Helper()
	token, _, err := e.auth.CreateLoginToken(context.Background(), e.owner)
	require.NoError(e.t, err)
	code, me := e.do("POST", "/api/auth/exchange", map[string]string{"token": token})
	require.Equal(e.t, http.StatusOK, code)
	e.csrf = me["csrf_token"].(string)
}

func errCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

func TestAuthFlow(t *testing.T) {
	e := newEnv(t, config.PanelAuthMagicLink)

	code, body := e.do("GET", "/api/me", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "unauthenticated", errCode(body))

	token, _, err := e.auth.CreateLoginToken(context.Background(), e.owner)
	require.NoError(t, err)
	code, _ = e.do("POST", "/api/auth/exchange", map[string]string{"token": token}, "Origin", "https://evil.example")
	assert.Equal(t, http.StatusForbidden, code, "чужой Origin")

	code, me := e.do("POST", "/api/auth/exchange", map[string]string{"token": token})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Ивановы", me["family"].(map[string]any)["name"])
	e.csrf = me["csrf_token"].(string)

	code, _ = e.do("POST", "/api/auth/exchange", map[string]string{"token": token})
	assert.Equal(t, http.StatusUnauthorized, code, "ссылка одноразовая")

	code, _ = e.do("GET", "/api/me", nil)
	assert.Equal(t, http.StatusOK, code, "сессия в cookie")

	code, _ = e.do("POST", "/api/auth/logout", nil)
	assert.Equal(t, http.StatusNoContent, code)
	code, _ = e.do("GET", "/api/me", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
}

func TestCSRFAndOrigin(t *testing.T) {
	e := newEnv(t, config.PanelAuthMagicLink)
	e.login()
	body := map[string]any{"amount": "100", "category": "еда"}

	code, resp := e.do("POST", "/api/expenses", body, "X-CSRF-Token", "")
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "bad_csrf", errCode(resp))

	code, resp = e.do("POST", "/api/expenses", body, "Origin", "https://evil.example")
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "bad_origin", errCode(resp))

	code, _ = e.do("POST", "/api/expenses", body)
	assert.Equal(t, http.StatusCreated, code)
}

func TestExpensesAPI(t *testing.T) {
	e := newEnv(t, config.PanelAuthMagicLink)
	e.login()

	code, list := e.do("GET", "/api/expenses", nil)
	require.Equal(t, http.StatusOK, code)
	assert.EqualValues(t, 2, list["count"], "только траты своей семьи")
	assert.Equal(t, "1550.00", list["total"], "деньги - строкой")

	code, list = e.do("GET", "/api/expenses?member_id=2&category=транспорт", nil)
	require.Equal(t, http.StatusOK, code)
	assert.EqualValues(t, 1, list["count"])

	code, sum := e.do("GET", "/api/expenses/summary", nil)
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, sum["by_member"], 2)

	code, _ = e.do("GET", "/api/expenses?from=16.10.2026", nil)
	assert.Equal(t, http.StatusBadRequest, code)

	// Создание: деньги строкой, лишние поля запрещены, чужой пользователь - ошибка.
	code, created := e.do("POST", "/api/expenses", map[string]any{"user_id": 2, "amount": "500.50", "category": "еда", "title": "шаурма"})
	require.Equal(t, http.StatusCreated, code)
	assert.Equal(t, "500.50", created["amount"])
	code, _ = e.do("POST", "/api/expenses", `{"amount":500,"category":"еда"}`)
	assert.Equal(t, http.StatusBadRequest, code, "число вместо строки")
	code, _ = e.do("POST", "/api/expenses", map[string]any{"amount": "1", "category": "еда", "family_id": 2})
	assert.Equal(t, http.StatusBadRequest, code, "family_id из тела не принимается")
	code, resp := e.do("POST", "/api/expenses", map[string]any{"user_id": 3, "amount": "1", "category": "еда"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "validation", errCode(resp))

	// Оптимистичная блокировка.
	id := strconv.Itoa(int(created["id"].(float64)))
	upd := map[string]any{"version": 1, "amount": "600", "category": "еда", "date": created["date"], "title": "шаурма"}
	code, _ = e.do("PUT", "/api/expenses/"+id, upd)
	assert.Equal(t, http.StatusOK, code)
	code, resp = e.do("PUT", "/api/expenses/"+id, upd)
	assert.Equal(t, http.StatusConflict, code, "версия устарела")
	assert.Equal(t, "conflict", errCode(resp))

	code, _ = e.do("DELETE", "/api/expenses/"+id, nil)
	assert.Equal(t, http.StatusNoContent, code)
}

func TestNoAccessToOtherFamily(t *testing.T) {
	e := newEnv(t, config.PanelAuthMagicLink)
	e.login()
	path := "/api/expenses/" + strconv.Itoa(int(e.foreignE))
	for _, c := range []struct {
		method string
		body   any
	}{
		{"GET", nil},
		{"PUT", map[string]any{"version": 1, "amount": "1", "category": "еда", "date": "2026-10-16"}},
		{"DELETE", nil},
	} {
		code, _ := e.do(c.method, path, c.body)
		assert.Equal(t, http.StatusNotFound, code, c.method)
	}
	code, _ := e.do("DELETE", "/api/members/3", nil)
	assert.Equal(t, http.StatusNotFound, code, "чужого участника не удалить")
}

func TestFamilyMembersInvitesAPI(t *testing.T) {
	e := newEnv(t, config.PanelAuthMagicLink)
	e.login()

	code, ms := e.do("GET", "/api/members", nil)
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, ms["items"], 2)

	code, _ = e.do("PATCH", "/api/members/2", map[string]string{"relation": "муж"})
	assert.Equal(t, http.StatusNoContent, code)

	code, inv := e.do("POST", "/api/invites", nil)
	require.Equal(t, http.StatusCreated, code)
	assert.Contains(t, inv["link"], "https://t.me/test_bot?start=")
	code, _ = e.do("DELETE", "/api/invites/"+strconv.Itoa(int(inv["id"].(float64))), nil)
	assert.Equal(t, http.StatusNoContent, code)

	code, resp := e.do("PUT", "/api/family", map[string]string{"name": "Ивановы", "timezone": "Mars/Olympus"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "validation", errCode(resp))

	// Передача владения закрывает панель бывшему владельцу.
	code, _ = e.do("POST", "/api/members/2/transfer-ownership", nil)
	assert.Equal(t, http.StatusNoContent, code)
	code, _ = e.do("GET", "/api/me", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
}

func TestNoneAuthModeActsAsOwner(t *testing.T) {
	e := newEnv(t, config.PanelAuthNone)
	code, me := e.do("GET", "/api/me", nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "none", me["auth_mode"])
	code, _ = e.do("POST", "/api/expenses", map[string]any{"amount": "10", "category": "еда"})
	assert.Equal(t, http.StatusCreated, code, "без CSRF в режиме none")
	code, _ = e.do("POST", "/api/expenses", map[string]any{"amount": "10", "category": "еда"}, "Origin", "https://evil.example")
	assert.Equal(t, http.StatusForbidden, code, "Origin проверяется всегда")
	code, _ = e.do("POST", "/api/auth/exchange", map[string]string{"token": "x"})
	assert.Equal(t, http.StatusNotFound, code, "входа по ссылке в режиме none нет")
}
