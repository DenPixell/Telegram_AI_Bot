// Конфигурация из переменных окружения (.env), включая доступ к LLM.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	TelegramToken string
	DatabaseURL   string
	DefaultTZ     string
	HTTP          HTTPConfig
	LLM           LLMConfig
}

type HTTPConfig struct {
	Addr string
}

// LLMConfig - любой OpenAI-совместимый провайдер (base URL + ключ + модель).
type LLMConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

func Load() (*Config, error) {
	cfg := &Config{
		TelegramToken: os.Getenv("TELEGRAM_TOKEN"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		DefaultTZ:     getenv("DEFAULT_TZ", "Europe/Moscow"),
		HTTP:          HTTPConfig{Addr: getenv("HTTP_ADDR", ":8080")},
		LLM: LLMConfig{
			BaseURL: os.Getenv("LLM_BASE_URL"),
			APIKey:  os.Getenv("LLM_API_KEY"),
			Model:   os.Getenv("LLM_MODEL"),
		},
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
