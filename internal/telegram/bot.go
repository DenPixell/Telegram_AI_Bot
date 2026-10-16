// Package telegram - адаптер Telegram: long polling (строго одна реплика), дедупликация
// апдейтов, очередь на пользователя, исходящий лимитер, разбиение длинных сообщений.
package telegram

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"expense-bot/internal/storage/postgres"
)

const (
	pollTimeout    = 50 * time.Second
	updateTimeout  = 90 * time.Second // на обработку одного апдейта (LLM + ретраи + fallback)
	maxPollBackoff = 30 * time.Second
)

type Bot struct {
	api     *API
	store   *postgres.Store
	handler *Handler
	// username заполняется из getMe; httpapi читает его для ссылок-приглашений.
	username atomic.Value

	mu     sync.Mutex
	queues map[int64]chan Update
	wg     sync.WaitGroup
}

func NewBot(api *API, store *postgres.Store, configuredUsername string) *Bot {
	b := &Bot{api: api, store: store, queues: map[int64]chan Update{}}
	b.username.Store(configuredUsername)
	return b
}

// SetHandler - обработчик создаётся после бота, потому что ему нужен Username.
func (b *Bot) SetHandler(h *Handler) { b.handler = h }

// Username - username бота без @, "" до первого успешного getMe (если не задан в конфиге).
func (b *Bot) Username() string {
	s, _ := b.username.Load().(string)
	return s
}

var commands = []Command{
	{Command: "start", Description: "Начать"},
	{Command: "help", Description: "Как пользоваться"},
	{Command: "invite", Description: "Пригласить в семью"},
	{Command: "panel", Description: "Веб-панель (для главы семьи)"},
	{Command: "tz", Description: "Часовой пояс"},
	{Command: "leave", Description: "Выйти из семьи"},
}

// Run опрашивает Telegram до отмены ctx, затем ждёт завершения начатых обработок
// не дольше drain. Неверный токен - фатальная ошибка.
func (b *Bot) Run(ctx context.Context, drain time.Duration) error {
	me, err := b.getMe(ctx)
	if err != nil {
		return err
	}
	if b.Username() == "" {
		b.username.Store(me.Username)
	}
	slog.InfoContext(ctx, "telegram bot started", "username", me.Username)
	if err := b.api.SetMyCommands(ctx, commands); err != nil {
		slog.WarnContext(ctx, "set commands failed", "err", err)
	}

	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		ups, err := b.api.GetUpdates(ctx, offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.Code == http.StatusConflict {
				// Второй экземпляр с тем же токеном: long polling допускает только одну реплику.
				slog.ErrorContext(ctx, "another bot instance is polling with the same token", "err", err)
			} else {
				slog.WarnContext(ctx, "get updates failed", "err", err, "retry_in", backoff.String())
			}
			sleep(ctx, backoff)
			backoff = min(backoff*2, maxPollBackoff)
			continue
		}
		backoff = time.Second
		for _, up := range ups {
			offset = up.UpdateID + 1
			b.dispatch(ctx, up)
		}
	}

	slog.Info("telegram polling stopped, draining in-flight updates")
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(drain):
		slog.Warn("telegram drain timeout, some updates were interrupted")
	}
	return nil
}

func (b *Bot) getMe(ctx context.Context) (User, error) {
	backoff := time.Second
	for {
		me, err := b.api.GetMe(ctx)
		if err == nil {
			return me, nil
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && (apiErr.Code == http.StatusUnauthorized || apiErr.Code == http.StatusNotFound) {
			return User{}, errors.New("telegram: invalid TELEGRAM_BOT_TOKEN")
		}
		if ctx.Err() != nil {
			return User{}, ctx.Err()
		}
		slog.WarnContext(ctx, "telegram getMe failed, retrying", "err", err, "retry_in", backoff.String())
		sleep(ctx, backoff)
		backoff = min(backoff*2, maxPollBackoff)
	}
}

// dispatch: дедупликация по update_id и очередь на пользователя - сообщения одного человека
// обрабатываются строго по порядку, разных людей - параллельно.
func (b *Bot) dispatch(ctx context.Context, up Update) {
	userID := updateUser(up)
	if userID == 0 {
		return
	}
	isNew, err := b.store.MarkUpdateProcessed(ctx, up.UpdateID)
	if err != nil {
		slog.ErrorContext(ctx, "dedup failed, skipping update", "update_id", up.UpdateID, "err", err)
		return
	}
	if !isNew {
		return // повторная доставка
	}

	b.mu.Lock()
	q, running := b.queues[userID]
	if !running {
		q = make(chan Update, 32)
		b.queues[userID] = q
	}
	select {
	case q <- up:
	default:
		slog.WarnContext(ctx, "user queue full, dropping update", "user_id", userID)
	}
	b.mu.Unlock()
	if !running {
		b.wg.Add(1)
		go b.drainQueue(context.WithoutCancel(ctx), userID, q)
	}
}

// drainQueue обрабатывает очередь пользователя и завершается, когда она пуста.
// Контекст без отмены: начатое сообщение дорабатывается при остановке сервера.
func (b *Bot) drainQueue(ctx context.Context, userID int64, q chan Update) {
	defer b.wg.Done()
	for {
		b.mu.Lock()
		var up Update
		select {
		case up = <-q:
		default:
			delete(b.queues, userID)
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()

		uctx, cancel := context.WithTimeout(ctx, updateTimeout)
		func() {
			defer cancel()
			defer func() {
				if v := recover(); v != nil {
					slog.ErrorContext(uctx, "panic while handling update", "update_id", up.UpdateID, "panic", v)
				}
			}()
			b.handler.HandleUpdate(uctx, up)
		}()
	}
}

func updateUser(up Update) int64 {
	switch {
	case up.Message != nil && up.Message.From != nil:
		return up.Message.From.ID
	case up.CallbackQuery != nil:
		return up.CallbackQuery.From.ID
	}
	return 0
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
