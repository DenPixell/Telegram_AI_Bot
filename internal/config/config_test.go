package config

import (
	"strings"
	"testing"
	_ "time/tzdata"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("DB_DSN", "postgres://app:app@localhost:5432/expenses")
	t.Setenv("LLM_BASE_URL", "https://openrouter.ai/api/v1")
	t.Setenv("LLM_MODEL", "openrouter/free")
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	t.Setenv("ALLOWED_TELEGRAM_IDS", "1,2")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Panel.AuthMode != PanelAuthMagicLink {
		t.Errorf("AuthMode = %q", cfg.Panel.AuthMode)
	}
	if cfg.Limits.InviteTTL.Hours() != 168 {
		t.Errorf("InviteTTL = %v", cfg.Limits.InviteTTL)
	}
	if len(cfg.Telegram.AllowedIDs) != 2 {
		t.Errorf("AllowedIDs = %v", cfg.Telegram.AllowedIDs)
	}
	if cfg.Fallback.Enabled() {
		t.Error("fallback must be disabled by default")
	}
}

func TestLoadInvalid(t *testing.T) {
	cases := map[string]struct {
		key, value, wantErr string
	}{
		"auth mode":        {"PANEL_AUTH_MODE", "basic", "PANEL_AUTH_MODE"},
		"agent mode":       {"AGENT_MODE", "chat", "AGENT_MODE"},
		"timezone":         {"APP_TZ", "Mars/Olympus", "APP_TZ"},
		"partial fallback": {"LLM_FALLBACK_BASE_URL", "https://api.groq.com/openai/v1", "LLM_FALLBACK_MODEL"},
		"bad url":          {"PUBLIC_URL", "localhost:3000", "PUBLIC_URL"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want mention of %s", err, tc.wantErr)
			}
		})
	}
}

func TestLoadMissingRequired(t *testing.T) {
	setRequired(t)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing TELEGRAM_BOT_TOKEN")
	}
}

func TestPlatformDefaults(t *testing.T) {
	setRequired(t)
	t.Setenv("PORT", "10000")
	t.Setenv("RENDER_EXTERNAL_URL", "https://expense-bot.onrender.com/")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":10000" || cfg.Panel.PublicURL != "https://expense-bot.onrender.com" {
		t.Fatalf("addr=%q public=%q", cfg.HTTPAddr, cfg.Panel.PublicURL)
	}

	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("PUBLIC_URL", "https://bot.example.com")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":9090" || cfg.Panel.PublicURL != "https://bot.example.com" {
		t.Fatal("явные HTTP_ADDR и PUBLIC_URL важнее значений хостинга")
	}
}

func TestWebhookModeValidation(t *testing.T) {
	setRequired(t)
	t.Setenv("TELEGRAM_MODE", "webhook")
	t.Setenv("PUBLIC_URL", "http://localhost:3000")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("webhook без HTTPS должен отклоняться: %v", err)
	}
	t.Setenv("PUBLIC_URL", "https://bot.example.com")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "has spaces!")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TELEGRAM_WEBHOOK_SECRET") {
		t.Fatalf("секрет с недопустимыми символами: %v", err)
	}
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "ok_secret-123")
	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
}
