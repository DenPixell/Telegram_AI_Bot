package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"expense-bot/internal/metrics"
)

// Лимиты Telegram: около 30 сообщений в секунду всего и 1 в секунду в один чат.
const (
	globalInterval = time.Second / 30
	chatInterval   = time.Second
	maxMessageLen  = 4096
	maxSendRetries = 3
	maxRetryAfter  = 30 * time.Second
)

// Sender - исходящие сообщения с лимитером и повтором по retry_after.
type Sender struct {
	api       *API
	onBlocked func(ctx context.Context, chatID int64) // 403: пользователь заблокировал бота

	mu       sync.Mutex
	nextAny  time.Time
	nextChat map[int64]time.Time
}

func NewSender(api *API, onBlocked func(ctx context.Context, chatID int64)) *Sender {
	return &Sender{api: api, onBlocked: onBlocked, nextChat: map[int64]time.Time{}}
}

// Send отправляет текст, при необходимости частями по 4096 символов. Клавиатура
// прикрепляется к последней части.
func (s *Sender) Send(ctx context.Context, chatID int64, text string, markup *ReplyMarkup) error {
	parts := splitMessage(text, maxMessageLen)
	for i, p := range parts {
		var m *ReplyMarkup
		if i == len(parts)-1 {
			m = markup
		}
		if err := s.sendOne(ctx, chatID, p, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sender) sendOne(ctx context.Context, chatID int64, text string, markup *ReplyMarkup) error {
	for attempt := 1; ; attempt++ {
		if err := s.wait(ctx, chatID); err != nil {
			return err
		}
		err := s.api.SendMessage(ctx, chatID, text, markup)
		if err == nil {
			return nil
		}
		code := "network"
		var e *APIError
		if errors.As(err, &e) {
			code = strconv.Itoa(e.Code)
		}
		metrics.TelegramSendErrors.Inc(code)
		if IsBlocked(err) {
			if s.onBlocked != nil {
				s.onBlocked(ctx, chatID)
			}
			return err
		}
		if e == nil || e.RetryAfter == 0 || attempt >= maxSendRetries || e.RetryAfter > maxRetryAfter {
			return err
		}
		slog.WarnContext(ctx, "telegram rate limited", "retry_after", e.RetryAfter.String())
		s.delay(chatID, e.RetryAfter)
	}
}

// wait резервирует ближайший слот с учётом общего и поштучного лимита.
func (s *Sender) wait(ctx context.Context, chatID int64) error {
	s.mu.Lock()
	now := time.Now()
	at := now
	if s.nextAny.After(at) {
		at = s.nextAny
	}
	if c := s.nextChat[chatID]; c.After(at) {
		at = c
	}
	s.nextAny = at.Add(globalInterval)
	s.nextChat[chatID] = at.Add(chatInterval)
	if len(s.nextChat) > 10_000 {
		for id, t := range s.nextChat {
			if t.Before(now) {
				delete(s.nextChat, id)
			}
		}
	}
	s.mu.Unlock()

	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func (s *Sender) delay(chatID int64, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := time.Now().Add(d); t.After(s.nextChat[chatID]) {
		s.nextChat[chatID] = t
	}
}

// splitMessage режет текст по границам строк, а слишком длинные строки - по символам.
func splitMessage(text string, limit int) []string {
	if utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}
	var parts []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, strings.TrimRight(cur.String(), "\n"))
			cur.Reset()
			curLen = 0
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		n := utf8.RuneCountInString(line)
		if curLen+n > limit {
			flush()
		}
		for n > limit {
			r := []rune(line)
			parts = append(parts, string(r[:limit]))
			line = string(r[limit:])
			n -= limit
		}
		cur.WriteString(line)
		curLen += n
	}
	flush()
	return parts
}
