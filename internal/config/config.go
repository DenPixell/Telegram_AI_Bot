// Package config загружает конфигурацию из переменных окружения и валидирует её при старте (fail fast).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/caarlos0/env/v11"
)

const (
	PanelAuthMagicLink = "magic_link"
	PanelAuthNone      = "none"

	AgentModeTools = "tools"
	AgentModeJSON  = "json"
)

type Config struct {
	Telegram Telegram
	Panel    Panel
	LLM      LLM
	Fallback LLMFallback
	Limits   Limits

	AppTZ    string `env:"APP_TZ" envDefault:"Europe/Moscow"`
	Currency string `env:"CURRENCY" envDefault:"RUB"`

	DBDSN    string `env:"DB_DSN,required,notEmpty"`
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
}

type Telegram struct {
	Token       string `env:"TELEGRAM_BOT_TOKEN,required,notEmpty"`
	BotUsername string `env:"BOT_USERNAME"` // пусто = берётся из getMe
	// APIURL - кастомный базовый URL Bot API (прокси/зеркало), пусто = api.telegram.org.
	APIURL     string  `env:"TELEGRAM_API_URL"`
	AllowedIDs []int64 `env:"ALLOWED_TELEGRAM_IDS" envSeparator:","` // пусто = регистрация открыта всем
}

type Panel struct {
	PublicURL string `env:"PUBLIC_URL" envDefault:"http://localhost:3000"`
	AuthMode  string `env:"PANEL_AUTH_MODE" envDefault:"magic_link"`
	FamilyID  int64  `env:"PANEL_FAMILY_ID"` // только для режима none, 0 = единственная семья
}

// LLM - любой OpenAI-совместимый провайдер.
type LLM struct {
	BaseURL         string        `env:"LLM_BASE_URL,required,notEmpty"`
	APIKey          string        `env:"LLM_API_KEY"` // может быть пустым (Ollama)
	Model           string        `env:"LLM_MODEL,required,notEmpty"`
	Timeout         time.Duration `env:"LLM_TIMEOUT" envDefault:"15s"`
	MaxOutputTokens int           `env:"LLM_MAX_OUTPUT_TOKENS" envDefault:"200"`
	MaxConcurrency  int           `env:"LLM_MAX_CONCURRENCY" envDefault:"4"`
	AgentMode       string        `env:"AGENT_MODE" envDefault:"tools"`
}

// LLMFallback - запасной провайдер, задаётся целиком или не задаётся.
type LLMFallback struct {
	BaseURL string `env:"LLM_FALLBACK_BASE_URL"`
	APIKey  string `env:"LLM_FALLBACK_API_KEY"`
	Model   string `env:"LLM_FALLBACK_MODEL"`
}

func (f LLMFallback) Enabled() bool { return f.BaseURL != "" }

type Limits struct {
	RatePerMin       int           `env:"RATE_LIMIT_PER_MIN" envDefault:"10"`
	LLMQuotaUser     int           `env:"LLM_DAILY_QUOTA_USER" envDefault:"100"`
	LLMQuotaFamily   int           `env:"LLM_DAILY_QUOTA_FAMILY" envDefault:"400"`
	MaxMessageLen    int           `env:"MAX_MESSAGE_LEN" envDefault:"300"`
	MaxFamilyMembers int           `env:"MAX_FAMILY_MEMBERS" envDefault:"10"`
	InviteTTL        time.Duration `env:"INVITE_TTL" envDefault:"168h"`
	InviteMaxUses    int           `env:"INVITE_MAX_USES" envDefault:"10"`
}

// Load читает конфиг из окружения и проверяет его.
func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parse env: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}

	check(c.Panel.AuthMode == PanelAuthMagicLink || c.Panel.AuthMode == PanelAuthNone,
		"PANEL_AUTH_MODE must be %q or %q, got %q", PanelAuthMagicLink, PanelAuthNone, c.Panel.AuthMode)
	check(isHTTPURL(c.Panel.PublicURL), "PUBLIC_URL must be an http(s) URL, got %q", c.Panel.PublicURL)
	check(c.Panel.FamilyID >= 0, "PANEL_FAMILY_ID must be >= 0")

	check(isHTTPURL(c.LLM.BaseURL), "LLM_BASE_URL must be an http(s) URL, got %q", c.LLM.BaseURL)
	check(c.LLM.Timeout > 0, "LLM_TIMEOUT must be > 0")
	check(c.LLM.MaxOutputTokens > 0, "LLM_MAX_OUTPUT_TOKENS must be > 0")
	check(c.LLM.MaxConcurrency > 0, "LLM_MAX_CONCURRENCY must be > 0")
	check(c.LLM.AgentMode == AgentModeTools || c.LLM.AgentMode == AgentModeJSON,
		"AGENT_MODE must be %q or %q, got %q", AgentModeTools, AgentModeJSON, c.LLM.AgentMode)

	fb := c.Fallback
	if fb.BaseURL != "" || fb.Model != "" || fb.APIKey != "" {
		check(isHTTPURL(fb.BaseURL), "LLM_FALLBACK_BASE_URL must be an http(s) URL when fallback is configured")
		check(fb.Model != "", "LLM_FALLBACK_MODEL is required when fallback is configured")
	}

	if c.Telegram.APIURL != "" {
		check(isHTTPURL(c.Telegram.APIURL), "TELEGRAM_API_URL must be an http(s) URL, got %q", c.Telegram.APIURL)
	}

	l := c.Limits
	check(l.RatePerMin > 0, "RATE_LIMIT_PER_MIN must be > 0")
	check(l.LLMQuotaUser > 0, "LLM_DAILY_QUOTA_USER must be > 0")
	check(l.LLMQuotaFamily > 0, "LLM_DAILY_QUOTA_FAMILY must be > 0")
	check(l.MaxMessageLen > 0, "MAX_MESSAGE_LEN must be > 0")
	check(l.MaxFamilyMembers > 0, "MAX_FAMILY_MEMBERS must be > 0")
	check(l.InviteTTL > 0, "INVITE_TTL must be > 0")
	check(l.InviteMaxUses > 0, "INVITE_MAX_USES must be > 0")

	if _, err := time.LoadLocation(c.AppTZ); err != nil {
		errs = append(errs, fmt.Errorf("APP_TZ %q is not a valid IANA zone: %w", c.AppTZ, err))
	}
	check(c.Currency != "", "CURRENCY must not be empty")

	return errors.Join(errs...)
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
