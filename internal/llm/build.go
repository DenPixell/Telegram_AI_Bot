package llm

import (
	"time"

	"expense-bot/internal/config"
)

// queueWait - сколько запрос ждёт свободный слот LLM_MAX_CONCURRENCY, прежде чем бот ответит
// «подождите». Меньше таймаута запроса, чтобы пользователь не ждал вдвое дольше.
const queueWait = 10 * time.Second

// New собирает клиента из конфига:
//
//	concurrency( fallback( breaker(retry(usage(primary))), breaker(retry(usage(fallback)))) )
//
// Каждый провайдер со своими ретраями и breaker. Учёт стоит внутри: записывается каждый
// реально отправленный HTTP-запрос (включая повторы), а отклонённые breaker - нет.
// Общий лимит параллелизма покрывает всю цепочку.
func New(cfg *config.Config, rec UsageRecorder) Client {
	chain := []Client{provider(OpenAIConfig{
		BaseURL: cfg.LLM.BaseURL, APIKey: cfg.LLM.APIKey, Model: cfg.LLM.Model,
		Timeout: cfg.LLM.Timeout, MaxTokens: cfg.LLM.MaxOutputTokens,
	}, rec)}
	if cfg.Fallback.Enabled() {
		chain = append(chain, provider(OpenAIConfig{
			BaseURL: cfg.Fallback.BaseURL, APIKey: cfg.Fallback.APIKey, Model: cfg.Fallback.Model,
			Timeout: cfg.LLM.Timeout, MaxTokens: cfg.LLM.MaxOutputTokens,
		}, rec))
	}
	return WithConcurrency(WithFallback(chain...), cfg.LLM.MaxConcurrency, queueWait)
}

func provider(cfg OpenAIConfig, rec UsageRecorder) Client {
	var c Client = NewOpenAI(cfg)
	if rec != nil {
		c = WithUsage(c, rec, cfg.Model)
	}
	return WithBreaker(WithRetry(c, DefaultRetry), DefaultBreaker)
}
