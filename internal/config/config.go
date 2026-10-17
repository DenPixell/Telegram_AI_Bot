// Package config загружает конфигурацию из переменных окружения и валидирует её при старте (fail fast).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

const (
	PanelAuthMagicLink = "magic_link"
	PanelAuthNone      = "none"

	AgentModeTools = "tools"
	AgentModeJSON  = "json"

	TelegramPolling = "polling"
	TelegramWebhook = "webhook"
)

type Config struct {
	Telegram Telegram
	Panel    Panel
	LLM      LLM
	Fallback LLMFallback
	Limits   Limits

	Synonyms Synonyms

	AppTZ    string `env:"APP_TZ" envDefault:"Europe/Moscow"`
	Currency string `env:"CURRENCY" envDefault:"RUB"`

	DBDSN    string `env:"DB_DSN,required,notEmpty"`
	HTTPAddr string `env:"HTTP_ADDR"` // пусто - ":$PORT" (Render, Heroku-подобные) или ":8080"
	// WebDir - собранная панель (web/dist): если задан, Go сам раздаёт её вместе с API.
	// Для хостингов с одним контейнером; в docker compose панель раздаёт nginx.
	WebDir string `env:"WEB_DIR"`
	// MetricsToken закрывает /metrics (Authorization: Bearer). Нужен, если сервер публичный
	// (WEB_DIR задан, перед ним нет nginx): без токена /metrics тогда не публикуется.
	MetricsToken string `env:"METRICS_TOKEN"`
	// AdminToken открывает HTTP API рассылки (POST /admin/broadcast); пусто - API выключен.
	AdminToken string `env:"ADMIN_TOKEN"`
	LogLevel   string `env:"LOG_LEVEL" envDefault:"info"`
}

type Telegram struct {
	// Mode: polling (по умолчанию) или webhook - для хостингов, которые усыпляют сервис
	// без входящего трафика (Render free): запрос Telegram будит сервис.
	Mode string `env:"TELEGRAM_MODE" envDefault:"polling"`
	// WebhookSecret проверяется в заголовке каждого запроса Telegram; пусто - выводится из токена.
	WebhookSecret string `env:"TELEGRAM_WEBHOOK_SECRET"`
	Token         string `env:"TELEGRAM_BOT_TOKEN,required,notEmpty"`
	BotUsername   string `env:"BOT_USERNAME"` // пусто = берётся из getMe
	// APIURL - кастомный базовый URL Bot API (прокси/зеркало), пусто = api.telegram.org.
	APIURL     string  `env:"TELEGRAM_API_URL"`
	AllowedIDs []int64 `env:"ALLOWED_TELEGRAM_IDS" envSeparator:","` // пусто = регистрация открыта всем
	// AdminIDs могут делать рассылку всем пользователям командой /broadcast.
	AdminIDs []int64 `env:"ADMIN_TELEGRAM_IDS" envSeparator:","`
}

type Panel struct {
	PublicURL string `env:"PUBLIC_URL"` // пусто - RENDER_EXTERNAL_URL или http://localhost:3000
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
	ReasoningEffort string        `env:"LLM_REASONING_EFFORT"` // для думающих моделей: none | minimal | low
	AgentMode       string        `env:"AGENT_MODE" envDefault:"tools"`
}

// LLMFallback - запасной провайдер, задаётся целиком или не задаётся.
type LLMFallback struct {
	BaseURL string `env:"LLM_FALLBACK_BASE_URL"`
	APIKey  string `env:"LLM_FALLBACK_API_KEY"`
	Model   string `env:"LLM_FALLBACK_MODEL"`
	// ReasoningEffort - отдельно: у запасной модели может не быть reasoning.
	ReasoningEffort string `env:"LLM_FALLBACK_REASONING_EFFORT"`
}

func (f LLMFallback) Enabled() bool { return f.BaseURL != "" }

// Synonyms - словарь и онлайн-тезаурус для распознавания категорий.
type Synonyms struct {
	Path          string        `env:"SYNONYMS_PATH" envDefault:"config/synonyms.yaml"`
	OnlineEnabled bool          `env:"THESAURUS_ENABLED" envDefault:"true"`
	OnlineURL     string        `env:"THESAURUS_URL" envDefault:"https://ru.wiktionary.org/w/api.php"`
	OnlineTimeout time.Duration `env:"THESAURUS_TIMEOUT" envDefault:"3s"`
}

type Limits struct {
	RatePerMin       int           `env:"RATE_LIMIT_PER_MIN" envDefault:"10"`
	LLMQuotaUser     int           `env:"LLM_DAILY_QUOTA_USER" envDefault:"100"`
	LLMQuotaFamily   int           `env:"LLM_DAILY_QUOTA_FAMILY" envDefault:"400"`
	LLMQuotaGlobal   int           `env:"LLM_DAILY_QUOTA_GLOBAL" envDefault:"5000"` // 0 = без глобального лимита
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
	cfg.applyPlatformDefaults(os.Getenv)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyPlatformDefaults берёт значения, которые выставляет хостинг: PORT для HTTP-сервера и
// RENDER_EXTERNAL_URL как публичный адрес. Явно заданные HTTP_ADDR и PUBLIC_URL важнее.
func (c *Config) applyPlatformDefaults(getenv func(string) string) {
	if c.HTTPAddr == "" {
		c.HTTPAddr = ":8080"
		if p := getenv("PORT"); p != "" {
			c.HTTPAddr = ":" + p
		}
	}
	if c.Panel.PublicURL == "" {
		c.Panel.PublicURL = "http://localhost:3000"
		if u := getenv("RENDER_EXTERNAL_URL"); u != "" {
			c.Panel.PublicURL = u
		}
	}
	c.Panel.PublicURL = strings.TrimRight(c.Panel.PublicURL, "/")
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
	check(c.Telegram.Mode == TelegramPolling || c.Telegram.Mode == TelegramWebhook,
		"TELEGRAM_MODE must be %q or %q, got %q", TelegramPolling, TelegramWebhook, c.Telegram.Mode)
	if c.Telegram.Mode == TelegramWebhook {
		// Telegram принимает webhook только по HTTPS.
		check(strings.HasPrefix(c.Panel.PublicURL, "https://"), "TELEGRAM_MODE=webhook requires an https PUBLIC_URL, got %q", c.Panel.PublicURL)
	}
	if s := c.Telegram.WebhookSecret; s != "" {
		check(webhookSecretRe.MatchString(s), "TELEGRAM_WEBHOOK_SECRET must be 1-256 chars of A-Z, a-z, 0-9, _ and -")
	}

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
	check(l.LLMQuotaGlobal >= 0, "LLM_DAILY_QUOTA_GLOBAL must be >= 0")
	check(l.MaxMessageLen > 0, "MAX_MESSAGE_LEN must be > 0")
	check(l.MaxFamilyMembers > 0, "MAX_FAMILY_MEMBERS must be > 0")
	check(l.InviteTTL > 0, "INVITE_TTL must be > 0")
	check(l.InviteMaxUses > 0, "INVITE_MAX_USES must be > 0")

	check(c.Synonyms.Path != "", "SYNONYMS_PATH must not be empty")
	if c.Synonyms.OnlineEnabled {
		check(isHTTPURL(c.Synonyms.OnlineURL), "THESAURUS_URL must be an http(s) URL, got %q", c.Synonyms.OnlineURL)
		check(c.Synonyms.OnlineTimeout > 0, "THESAURUS_TIMEOUT must be > 0")
	}

	if _, err := time.LoadLocation(c.AppTZ); err != nil {
		errs = append(errs, fmt.Errorf("APP_TZ %q is not a valid IANA zone: %w", c.AppTZ, err))
	}
	check(c.Currency != "", "CURRENCY must not be empty")
	check(c.AdminToken == "" || len(c.AdminToken) >= 16, "ADMIN_TOKEN must be at least 16 characters")

	return errors.Join(errs...)
}

var webhookSecretRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
