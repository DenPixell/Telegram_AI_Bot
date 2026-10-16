package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

// ---------- ретраи ----------

type RetryConfig struct {
	Attempts      int           // всего попыток, включая первую
	BaseDelay     time.Duration // первая пауза, дальше удваивается с джиттером
	MaxDelay      time.Duration // потолок паузы, в том числе для Retry-After
	MaxRetryAfter time.Duration // Retry-After больше этого - не ждём, отдаём ошибку (уйдём в fallback)
}

var DefaultRetry = RetryConfig{Attempts: 2, BaseDelay: 300 * time.Millisecond, MaxDelay: 2 * time.Second, MaxRetryAfter: 5 * time.Second}

type retrying struct {
	next  Client
	cfg   RetryConfig
	sleep func(context.Context, time.Duration) error
}

// WithRetry повторяет временные ошибки (429, 5xx, таймауты, обрыв, мусорный ответ).
func WithRetry(c Client, cfg RetryConfig) Client {
	return &retrying{next: c, cfg: cfg, sleep: sleepCtx}
}

func (r *retrying) Name() string { return r.next.Name() }

func (r *retrying) Complete(ctx context.Context, req Request) (Response, error) {
	delay := r.cfg.BaseDelay
	var err error
	for attempt := 1; ; attempt++ {
		var resp Response
		resp, err = r.next.Complete(ctx, req)
		if err == nil || attempt >= r.cfg.Attempts || !IsRetryable(err) || ctx.Err() != nil {
			return resp, err
		}
		wait := jitter(delay)
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
			if apiErr.RetryAfter > r.cfg.MaxRetryAfter {
				return resp, err // ждать долго - лучше сразу запасной провайдер
			}
			wait = apiErr.RetryAfter
		}
		wait = min(wait, r.cfg.MaxDelay)
		slog.WarnContext(ctx, "llm retry", "provider", r.next.Name(), "attempt", attempt, "wait", wait.String(), "err", err)
		if sleepErr := r.sleep(ctx, wait); sleepErr != nil {
			return resp, err
		}
		delay *= 2
	}
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(rand.Int64N(int64(d)))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ---------- circuit breaker ----------

type BreakerConfig struct {
	Failures int           // ошибок подряд до размыкания
	Cooldown time.Duration // сколько провайдер исключён; затем один пробный запрос
}

var DefaultBreaker = BreakerConfig{Failures: 5, Cooldown: 30 * time.Second}

type breakerState int

const (
	closed breakerState = iota
	open
	halfOpen
)

type breaker struct {
	next Client
	cfg  BreakerConfig
	now  func() time.Time

	mu        sync.Mutex
	state     breakerState
	failures  int
	openUntil time.Time
	probing   bool
}

// WithBreaker исключает провайдера на Cooldown после Failures ошибок подряд: временных
// (429, 5xx, таймауты) и неисправности провайдера (ключ, модель, баланс). Ошибки конкретного
// запроса (400) не считаются.
func WithBreaker(c Client, cfg BreakerConfig) Client {
	return &breaker{next: c, cfg: cfg, now: time.Now}
}

func (b *breaker) Name() string { return b.next.Name() }

func (b *breaker) Complete(ctx context.Context, req Request) (Response, error) {
	if !b.acquire() {
		return Response{}, fmt.Errorf("%s: %w", b.next.Name(), ErrCircuitOpen)
	}
	resp, err := b.next.Complete(ctx, req)
	b.record(ctx, err)
	return resp, err
}

func (b *breaker) acquire() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case open:
		if b.now().Before(b.openUntil) {
			return false
		}
		b.state = halfOpen
		fallthrough
	case halfOpen:
		if b.probing {
			return false // пробный запрос уже идёт
		}
		b.probing = true
	}
	return true
}

func (b *breaker) record(ctx context.Context, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	wasProbe := b.probing
	b.probing = false
	// Успех, ошибка конкретного запроса (400) и отмена контекста - не сбой провайдера.
	if err == nil || !countsAsFailure(err) {
		if b.state != closed {
			slog.InfoContext(ctx, "llm circuit closed", "provider", b.next.Name())
		}
		b.state, b.failures = closed, 0
		return
	}
	b.failures++
	if wasProbe || b.failures >= b.cfg.Failures {
		b.state, b.openUntil, b.failures = open, b.now().Add(b.cfg.Cooldown), 0
		slog.WarnContext(ctx, "llm circuit opened", "provider", b.next.Name(), "cooldown", b.cfg.Cooldown.String())
	}
}

// ---------- fallback ----------

type fallback struct {
	chain []Client
}

// WithFallback пробует провайдеров по порядку, пока один не ответит. Если не ответил никто -
// ошибка оборачивает ErrUnavailable и ошибки всех провайдеров.
func WithFallback(chain ...Client) Client {
	return &fallback{chain: chain}
}

func (f *fallback) Name() string {
	names := make([]string, len(f.chain))
	for i, c := range f.chain {
		names[i] = c.Name()
	}
	return strings.Join(names, " -> ")
}

func (f *fallback) Complete(ctx context.Context, req Request) (Response, error) {
	errs := make([]error, 0, len(f.chain))
	for i, c := range f.chain {
		resp, err := c.Complete(ctx, req)
		if err == nil {
			if i > 0 {
				slog.InfoContext(ctx, "llm fallback used", "provider", c.Name())
			}
			return resp, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return Response{}, fmt.Errorf("%w: %w", ErrUnavailable, errors.Join(errs...))
}

// ---------- лимит параллелизма ----------

type limited struct {
	next    Client
	sem     chan struct{}
	maxWait time.Duration
}

// WithConcurrency ограничивает число одновременных запросов (LLM_MAX_CONCURRENCY; для
// GigaChat - 1 поток). Запрос ждёт слот не дольше maxWait, иначе ErrBusy.
func WithConcurrency(c Client, n int, maxWait time.Duration) Client {
	return &limited{next: c, sem: make(chan struct{}, max(n, 1)), maxWait: maxWait}
}

func (l *limited) Name() string { return l.next.Name() }

func (l *limited) Complete(ctx context.Context, req Request) (Response, error) {
	t := time.NewTimer(l.maxWait)
	defer t.Stop()
	select {
	case l.sem <- struct{}{}:
	case <-t.C:
		return Response{}, ErrBusy
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	defer func() { <-l.sem }()
	return l.next.Complete(ctx, req)
}
