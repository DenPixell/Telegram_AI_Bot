package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"unicode/utf8"

	"expense-bot/internal/domain"
	"expense-bot/internal/metrics"
	"expense-bot/internal/storage/postgres"
)

// progressEvery - как часто сохранять счётчики отправленных в БД.
const progressEvery = 25

// Broadcaster отправляет сообщение всем активным пользователям бота (техобслуживание,
// новости). Отправка идёт в фоне через общий Sender, поэтому лимиты Telegram соблюдаются,
// а заблокировавшие бота помечаются неактивными и в следующий раз не получают рассылку.
type Broadcaster struct {
	store  *postgres.Store
	sender *Sender
	appCtx context.Context // рассылка живёт, пока работает приложение
	wg     sync.WaitGroup
}

func NewBroadcaster(appCtx context.Context, store *postgres.Store, sender *Sender) *Broadcaster {
	return &Broadcaster{store: store, sender: sender, appCtx: appCtx}
}

// ErrEmptyBroadcast - пустой текст или длиннее лимита (ошибка валидации: в API это 400).
var ErrEmptyBroadcast = fmt.Errorf("%w: текст рассылки должен быть от 1 до %d символов", domain.ErrValidation, domain.MaxBroadcastLen)

// Draft создаёт черновик и считает получателей - для предпросмотра с подтверждением.
func (b *Broadcaster) Draft(ctx context.Context, text string, adminID int64) (domain.Broadcast, int, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > domain.MaxBroadcastLen {
		return domain.Broadcast{}, 0, ErrEmptyBroadcast
	}
	bc, err := b.store.CreateBroadcast(ctx, text, adminID)
	if err != nil {
		return domain.Broadcast{}, 0, err
	}
	n, err := b.store.CountActiveUsers(ctx)
	return bc, n, err
}

// Start запускает отправку черновика в фоне. ErrConflict - уже идёт другая рассылка,
// ErrNotFound - черновик уже отправлен или отменён (повторное нажатие кнопки).
func (b *Broadcaster) Start(ctx context.Context, id int64) (domain.Broadcast, error) {
	ids, err := b.store.ActiveUserIDs(ctx)
	if err != nil {
		return domain.Broadcast{}, err
	}
	bc, err := b.store.StartBroadcast(ctx, id, len(ids))
	if err != nil {
		return domain.Broadcast{}, err
	}
	slog.InfoContext(ctx, "broadcast started", "id", bc.ID, "recipients", len(ids))
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.run(b.appCtx, bc, ids)
	}()
	return bc, nil
}

// Send - черновик и сразу отправка (HTTP API, где подтверждение - сам запрос).
func (b *Broadcaster) Send(ctx context.Context, text string) (domain.Broadcast, error) {
	bc, _, err := b.Draft(ctx, text, 0)
	if err != nil {
		return domain.Broadcast{}, err
	}
	return b.Start(ctx, bc.ID)
}

func (b *Broadcaster) Cancel(ctx context.Context, id int64) error {
	return b.store.CancelBroadcast(ctx, id)
}

func (b *Broadcaster) Status(ctx context.Context, id int64) (domain.Broadcast, error) {
	return b.store.GetBroadcast(ctx, id)
}

// Wait ждёт завершения начатых рассылок (тесты и остановка сервера).
func (b *Broadcaster) Wait() { b.wg.Wait() }

func (b *Broadcaster) run(ctx context.Context, bc domain.Broadcast, ids []int64) {
	sent, failed := 0, 0
	for i, id := range ids {
		if ctx.Err() != nil {
			slog.Warn("broadcast interrupted by shutdown", "id", bc.ID, "sent", sent, "left", len(ids)-i)
			break
		}
		if err := b.sender.Send(ctx, id, bc.Text, nil); err != nil {
			failed++
			result := "failed"
			if IsBlocked(err) {
				result = "blocked" // Sender уже пометил пользователя неактивным
			}
			metrics.BroadcastMessages.Inc(result)
		} else {
			sent++
			metrics.BroadcastMessages.Inc("sent")
		}
		if (i+1)%progressEvery == 0 {
			if err := b.store.UpdateBroadcastProgress(context.WithoutCancel(ctx), bc.ID, sent, failed); err != nil {
				slog.Warn("broadcast progress not saved", "id", bc.ID, "err", err)
			}
		}
	}
	// Итог сохраняется и при остановке сервера: контекст без отмены.
	done, err := b.store.FinishBroadcast(context.WithoutCancel(ctx), bc.ID, sent, failed)
	if err != nil {
		slog.Error("broadcast finish not saved", "id", bc.ID, "err", err)
		return
	}
	slog.Info("broadcast finished", "id", bc.ID, "sent", sent, "failed", failed)
	if bc.CreatedBy != 0 && ctx.Err() == nil {
		_ = b.sender.Send(ctx, bc.CreatedBy, Report(done), nil)
	}
}

// Report - отчёт администратору.
func Report(bc domain.Broadcast) string {
	s := fmt.Sprintf("📢 Рассылка №%d завершена: доставлено %d из %d.", bc.ID, bc.Sent, bc.Total)
	if bc.Failed > 0 {
		s += fmt.Sprintf("\nНе доставлено: %d (чаще всего - пользователь заблокировал бота).", bc.Failed)
	}
	return s
}
