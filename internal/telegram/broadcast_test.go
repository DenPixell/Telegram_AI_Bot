package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
	"expense-bot/internal/pipeline"
	"expense-bot/internal/storage/postgres"
	"expense-bot/internal/testutil/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

// fakeTelegram - Bot API, который запоминает отправленные сообщения;
// чатам из blocked отвечает 403, как будто пользователь заблокировал бота.
type fakeTelegram struct {
	mu      sync.Mutex
	sent    map[int64][]sentMsg
	blocked map[int64]bool
}

type sentMsg struct {
	Text   string
	Markup *ReplyMarkup
}

func newFakeTelegram(t *testing.T, blocked ...int64) (*fakeTelegram, *API) {
	f := &fakeTelegram{sent: map[int64][]sentMsg{}, blocked: map[int64]bool{}}
	for _, id := range blocked {
		f.blocked[id] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			ChatID      int64        `json:"chat_id"`
			Text        string       `json:"text"`
			ReplyMarkup *ReplyMarkup `json:"reply_markup"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.blocked[p.ChatID] {
			_, _ = io.WriteString(w, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
			return
		}
		if p.ChatID != 0 {
			f.sent[p.ChatID] = append(f.sent[p.ChatID], sentMsg{Text: p.Text, Markup: p.ReplyMarkup})
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	t.Cleanup(srv.Close)
	return f, NewAPI(srv.URL, "TEST")
}

func (f *fakeTelegram) messages(chat int64) []sentMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMsg(nil), f.sent[chat]...)
}

func users(t *testing.T, s *postgres.Store, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		_, err := s.UpsertUser(context.Background(), id, "u", "")
		require.NoError(t, err)
	}
}

func newBroadcaster(t *testing.T, blocked ...int64) (*postgres.Store, *fakeTelegram, *Broadcaster, *Sender) {
	t.Helper()
	s, _ := pgtest.New(t)
	tg, api := newFakeTelegram(t, blocked...)
	sender := NewSender(api, func(ctx context.Context, chatID int64) { _ = s.SetUserActive(ctx, chatID, false) })
	return s, tg, NewBroadcaster(context.Background(), s, sender), sender
}

func TestBroadcastDeliversToActiveUsersAndReports(t *testing.T) {
	ctx := context.Background()
	s, tg, b, _ := newBroadcaster(t, 30)
	users(t, s, 1, 10, 20, 30, 40)
	require.NoError(t, s.SetUserActive(ctx, 40, false))

	draft, n, err := b.Draft(ctx, "  Сегодня с 22:00 до 23:00 бот на техобслуживании.  ", 1)
	require.NoError(t, err)
	assert.Equal(t, 4, n, "получатели - активные пользователи, включая админа")
	assert.Equal(t, "Сегодня с 22:00 до 23:00 бот на техобслуживании.", draft.Text)

	_, err = b.Start(ctx, draft.ID)
	require.NoError(t, err)
	b.Wait()

	for _, id := range []int64{10, 20} {
		msgs := tg.messages(id)
		require.Len(t, msgs, 1, id)
		assert.Equal(t, draft.Text, msgs[0].Text)
	}
	assert.Empty(t, tg.messages(40), "неактивные не получают")

	done, err := b.Status(ctx, draft.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.BroadcastDone, done.Status)
	assert.Equal(t, [3]int{4, 3, 1}, [3]int{done.Total, done.Sent, done.Failed})

	u, err := s.GetUser(ctx, 30)
	require.NoError(t, err)
	assert.False(t, u.IsActive, "заблокировавший бота помечен неактивным")

	admin := tg.messages(1)
	require.Len(t, admin, 2, "сама рассылка и отчёт")
	assert.Contains(t, admin[1].Text, "доставлено 3 из 4")
	assert.Contains(t, admin[1].Text, "Не доставлено: 1")
}

func TestBroadcastValidationAndCancel(t *testing.T) {
	ctx := context.Background()
	s, tg, b, _ := newBroadcaster(t)
	users(t, s, 1, 2)

	_, _, err := b.Draft(ctx, "   ", 1)
	assert.ErrorIs(t, err, domain.ErrValidation)
	_, _, err = b.Draft(ctx, strings.Repeat("я", domain.MaxBroadcastLen+1), 1)
	assert.ErrorIs(t, err, domain.ErrValidation)

	draft, _, err := b.Draft(ctx, "Отменённая", 1)
	require.NoError(t, err)
	require.NoError(t, b.Cancel(ctx, draft.ID))
	_, err = b.Start(ctx, draft.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound, "отменённую не отправить")
	assert.Empty(t, tg.messages(2))
}

func TestBroadcastHTTPSend(t *testing.T) {
	ctx := context.Background()
	s, tg, b, _ := newBroadcaster(t)
	users(t, s, 5)
	bc, err := b.Send(ctx, "Из HTTP API")
	require.NoError(t, err)
	b.Wait()
	assert.Len(t, tg.messages(5), 1)
	done, _ := b.Status(ctx, bc.ID)
	assert.Equal(t, 1, done.Sent)
}

func TestBroadcastAdminCommand(t *testing.T) {
	ctx := context.Background()
	s, tg, b, sender := newBroadcaster(t)
	users(t, s, 1, 2)
	h := NewHandler(HandlerDeps{
		Store: s, Sender: sender, API: NewAPI("http://127.0.0.1:9", "x"), Broadcast: b,
		AdminIDs: []int64{1}, Limiter: pipeline.NewRateLimiter(100, time.Minute), BotUsername: func() string { return "bot" },
	})
	msg := func(from int64, text string) *Message {
		return &Message{From: &User{ID: from}, Chat: Chat{ID: from, Type: "private"}, Text: text}
	}

	require.NoError(t, h.message(ctx, msg(1, "/broadcast")))
	assert.Contains(t, tg.messages(1)[0].Text, "Напишите текст после команды")

	require.NoError(t, h.message(ctx, msg(1, "/broadcast Завтра бот недоступен с 10 до 11")))
	preview := tg.messages(1)[1]
	assert.Contains(t, preview.Text, "получат 2 пользователя")
	assert.Contains(t, preview.Text, "Завтра бот недоступен с 10 до 11")
	require.NotNil(t, preview.Markup)
	sendData := preview.Markup.InlineKeyboard[0][0].CallbackData

	// Не-админ не может нажать чужую кнопку.
	cq := func(from int64, data string) *CallbackQuery {
		return &CallbackQuery{ID: "q", From: User{ID: from}, Data: data, Message: &Message{MessageID: 1, Chat: Chat{ID: from, Type: "private"}}}
	}
	require.NoError(t, h.callback(ctx, cq(2, sendData)))
	assert.Empty(t, tg.messages(2), "рассылка не запущена не-админом")

	require.NoError(t, h.callback(ctx, cq(1, sendData)))
	b.Wait()
	assert.Contains(t, tg.messages(1)[2].Text, "запущена")
	assert.Equal(t, "Завтра бот недоступен с 10 до 11", tg.messages(2)[0].Text)

	require.NoError(t, h.callback(ctx, cq(1, sendData)))
	last := tg.messages(1)
	assert.Contains(t, last[len(last)-1].Text, "уже отправлена или отменена", "повторное нажатие не задваивает")
}
