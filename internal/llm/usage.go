package llm

import (
	"context"
	"log/slog"
	"time"

	"expense-bot/internal/metrics"
)

// UsageRecord - одна попытка запроса к провайдеру, для квот и контроля стоимости.
type UsageRecord struct {
	Caller           Caller
	Provider         string
	Model            string
	PromptTokens     int
	CompletionTokens int
	Latency          time.Duration
	OK               bool
}

// UsageRecorder сохраняет учёт (таблица llm_usage).
type UsageRecorder interface {
	RecordUsage(ctx context.Context, r UsageRecord) error
}

type recording struct {
	next  Client
	rec   UsageRecorder
	model string
}

// WithUsage записывает каждый запрос, включая неудачные: квоты считают запросы, а не успехи.
// Ставится вплотную к HTTP-клиенту (см. New), чтобы учитывать повторы и не учитывать
// запросы, которые breaker не отправил. Текст запроса не логируется.
func WithUsage(c Client, rec UsageRecorder, model string) Client {
	return &recording{next: c, rec: rec, model: model}
}

func (r *recording) Name() string { return r.next.Name() }

func (r *recording) Complete(ctx context.Context, req Request) (Response, error) {
	start := time.Now()
	resp, err := r.next.Complete(ctx, req)
	model := resp.Model
	if model == "" {
		model = r.model
	}
	rec := UsageRecord{
		Caller:           CallerFrom(ctx),
		Provider:         r.next.Name(),
		Model:            model,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		Latency:          time.Since(start),
		OK:               err == nil,
	}
	// Учёт не должен ломать ответ пользователю: отдельный короткий контекст и только лог при ошибке.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if saveErr := r.rec.RecordUsage(saveCtx, rec); saveErr != nil {
		slog.WarnContext(ctx, "record llm usage failed", "err", saveErr)
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	metrics.LLMRequests.Inc(rec.Provider, result)
	metrics.LLMLatency.ObserveDuration(rec.Latency, rec.Provider)
	metrics.LLMTokens.Add(float64(rec.PromptTokens), rec.Provider, "prompt")
	metrics.LLMTokens.Add(float64(rec.CompletionTokens), rec.Provider, "completion")
	slog.InfoContext(ctx, "llm request", "provider", rec.Provider, "model", rec.Model, "ok", rec.OK,
		"latency", rec.Latency.String(), "prompt_tokens", rec.PromptTokens, "completion_tokens", rec.CompletionTokens)
	return resp, err
}
