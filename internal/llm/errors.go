package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
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
)

// APIError - ответ провайдера с HTTP-статусом ошибки.
type APIError struct {
	Provider   string
	Status     int
	Message    string        // из тела ответа, обрезано
	RetryAfter time.Duration // из заголовка Retry-After, 0 - нет
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: http %d: %s", e.Provider, e.Status, e.Message)
}

// Retryable: 408, 409, 425, 429 и 5xx - временные; остальные 4xx - ошибка запроса или ключа.
func (e *APIError) Retryable() bool {
	switch e.Status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}
	return e.Status >= 500
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
