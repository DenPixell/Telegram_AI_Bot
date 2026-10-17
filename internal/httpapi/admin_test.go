package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"expense-bot/internal/domain"
)

type fakeBroadcaster struct {
	err  error
	text string
}

func (f *fakeBroadcaster) Send(_ context.Context, text string) (domain.Broadcast, error) {
	f.text = text
	if f.err != nil {
		return domain.Broadcast{}, f.err
	}
	return domain.Broadcast{ID: 7, Text: text, Status: domain.BroadcastSending, Total: 42}, nil
}

func (f *fakeBroadcaster) Status(_ context.Context, id int64) (domain.Broadcast, error) {
	if id != 7 {
		return domain.Broadcast{}, domain.ErrNotFound
	}
	return domain.Broadcast{ID: 7, Status: domain.BroadcastDone, Total: 42, Sent: 41, Failed: 1}, nil
}

const adminToken = "admin-token-0123456789"

func adminReq(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminBroadcastAPI(t *testing.T) {
	fb := &fakeBroadcaster{}
	h := New(Deps{DB: fakePinger{}, Broadcast: fb, AdminToken: adminToken}).MustHandler()
	body := `{"text":"Техобслуживание с 22:00 до 23:00"}`

	assert.Equal(t, http.StatusUnauthorized, adminReq(h, "POST", "/admin/broadcast", body, "").Code)
	assert.Equal(t, http.StatusUnauthorized, adminReq(h, "POST", "/admin/broadcast", body, "wrong").Code)

	rec := adminReq(h, "POST", "/admin/broadcast", body, adminToken)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Contains(t, rec.Body.String(), `"total":42`)
	assert.Equal(t, "Техобслуживание с 22:00 до 23:00", fb.text)

	rec = adminReq(h, "GET", "/admin/broadcast/7", "", adminToken)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"done"`)
	assert.Equal(t, http.StatusNotFound, adminReq(h, "GET", "/admin/broadcast/8", "", adminToken).Code)

	fb.err = domain.ErrConflict
	assert.Equal(t, http.StatusConflict, adminReq(h, "POST", "/admin/broadcast", body, adminToken).Code)
	fb.err = domain.ErrValidation
	assert.Equal(t, http.StatusBadRequest, adminReq(h, "POST", "/admin/broadcast", `{"text":""}`, adminToken).Code)
}

func TestAdminBroadcastDisabledWithoutToken(t *testing.T) {
	h := New(Deps{DB: fakePinger{}, Broadcast: &fakeBroadcaster{}}).MustHandler()
	rec := adminReq(h, "POST", "/admin/broadcast", `{"text":"x"}`, "anything")
	assert.NotEqual(t, http.StatusAccepted, rec.Code, "без ADMIN_TOKEN API рассылки нет")
}
