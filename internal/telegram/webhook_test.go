package telegram

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWebhookRejectsWithoutSecret(t *testing.T) {
	h := NewBot(nil, nil, "").WebhookHandler("s3cret")
	for name, hdr := range map[string]string{"no header": "", "wrong": "other", "prefix": "s3cre"} {
		req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", strings.NewReader(`{"update_id":1}`))
		if hdr != "" {
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", hdr)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, name)
	}

	req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", strings.NewReader(`{broken`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s3cret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWebhookSecret(t *testing.T) {
	assert.Equal(t, "configured", WebhookSecret("configured", "token"))
	derived := WebhookSecret("", "123:ABC")
	assert.Equal(t, derived, WebhookSecret("", "123:ABC"), "стабилен между рестартами")
	assert.NotEqual(t, derived, WebhookSecret("", "456:DEF"))
	assert.NotContains(t, derived, "ABC", "токен не раскрывается")
	assert.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`), derived, "символы, которые принимает Telegram")
}
