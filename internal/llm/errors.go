package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

var (
	// ErrUnavailable - ни один провайдер не ответил (все упали или их breaker разомкнут).
	// Бот отвечает «ИИ временно недоступен» и пробует regexp-деградацию.
	ErrUnavailable = errors.New("llm unavailable")
	// ErrBusy - очередь к LLM переполнена: не дождались слота за отведённое время.
	ErrBusy = errors.New("llm busy")
	// ErrCircuitOpen - breaker провайдера разомкнут, запрос не отправлялся.
	ErrCircuitOpen = errors.New("circuit open")
	// ErrBadResponse - ответ не разбирается или в нём нет вариантов.
	ErrBadResponse = errors.New("bad llm response")
	// ErrOutputLimit - лимит токенов кончился до ответа (обычно ушёл на размышления модели).
	// Повтор с теми же параметрами не поможет: нужен LLM_REASONING_EFFORT или больший
	// LLM_MAX_OUTPUT_TOKENS. Не повторяется, fallback пробует следующего провайдера.
	ErrOutputLimit = errors.New("output token limit reached before any answer")
)

// APIError - ответ провайдера с HTTP-статусом ошибки.
type APIError struct {
	Provider   string
	Status     int
	Message    string        // из тела ответа, обрезано
	Code       string        // код провайдера: "insufficient_quota", "invalid_api_key", ...
	RetryAfter time.Duration // из заголовка Retry-After, 0 - нет
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: http %d: %s", e.Provider, e.Status, e.Message)
}

// Retryable: 408, 409, 425, 429 и 5xx - временные; остальные 4xx - ошибка запроса или ключа.
// Исчерпанный баланс тоже приходит как 429, но повторять его бесполезно.
func (e *APIError) Retryable() bool {
	if e.quotaExhausted() {
		return false
	}
	switch e.Status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}
	return e.Status >= 500
}

func (e *APIError) quotaExhausted() bool {
	m := strings.ToLower(e.Message)
	return e.Code == "insufficient_quota" || strings.Contains(m, "no credits") || strings.Contains(m, "exceeded your current quota")
}

// ProviderFault - ошибка, после которой провайдер бесполезен до вмешательства человека:
// неверный ключ (401/403), модель не найдена (404), кончился баланс. Повторять запрос нет
// смысла, но breaker должен исключить провайдера, чтобы не тратить время каждого сообщения.
func (e *APIError) ProviderFault() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden ||
		e.Status == http.StatusNotFound || e.quotaExhausted()
}

// IsRetryable сообщает, имеет ли смысл повторить запрос к тому же провайдеру.
func IsRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable()
	}
	if errors.Is(err, ErrBadResponse) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// countsAsFailure - учитывать ли ошибку в breaker: временные сбои и неисправность провайдера,
// но не ошибки конкретного запроса (400) и не отмену.
func countsAsFailure(err error) bool {
	if IsRetryable(err) {
		return true
	}
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.ProviderFault()
}
