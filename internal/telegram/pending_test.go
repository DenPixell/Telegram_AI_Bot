package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/agent"
	"expense-bot/internal/domain"
	"expense-bot/internal/limits"
	"expense-bot/internal/llm"
	"expense-bot/internal/nlp/category"
	"expense-bot/internal/nlp/filter"
	"expense-bot/internal/nlp/lexicon"
	"expense-bot/internal/onboarding"
	"expense-bot/internal/service"
)

// Регрессия из проверки по ТЗ: новый пользователь первым сообщением пишет «кофе 350» -
// трата не должна теряться из-за регистрации.
func TestFirstExpenseOfNewUserIsRecordedAfterOnboarding(t *testing.T) {
	ctx := context.Background()
	s, tg, b, sender := newBroadcaster(t)
	fams := service.NewFamilies(s, service.FamiliesConfig{MaxMembers: 10, InviteTTL: time.Hour, InviteMaxUses: 10, DefaultTZ: "Europe/Moscow"})
	exp := service.NewExpenses(s, "Europe/Moscow")
	dict, err := lexicon.LoadDictionary("../../config/synonyms.yaml")
	require.NoError(t, err)
	cat := category.NewCategorizer(dict, nil, nil, time.Second)
	ag := agent.New(llm.NewFake(), exp, dict, cat, filter.NewAnalyzer(dict, cat, 300), nil, agent.Config{Mode: "tools", MaxTokens: 200})
	h := NewHandler(HandlerDeps{
		Store: s, Families: fams, Agent: ag, Sender: sender, API: NewAPI("http://127.0.0.1:9", "x"), Broadcast: b,
		FSM:     onboarding.New(s, fams, "Europe/Moscow", time.Hour, func() string { return "bot" }),
		Limiter: limits.NewRateLimiter(100, time.Minute), BotUsername: func() string { return "bot" }, AppTZ: "Europe/Moscow",
	})
	say := func(text string) string {
		t.Helper()
		before := len(tg.messages(7))
		require.NoError(t, h.message(ctx, &Message{From: &User{ID: 7, FirstName: "Аня"}, Chat: Chat{ID: 7, Type: "private"}, Text: text}))
		all := tg.messages(7)
		out := ""
		for _, m := range all[before:] {
			out += m.Text + "\n"
		}
		return out
	}

	assert.Contains(t, say("кофе 350"), "Запишу «кофе 350»")
	say("создать")
	say("Ивановы")
	reply := say("Москва")
	assert.Contains(t, reply, "Готово!")
	assert.Contains(t, reply, "Записал: кофе — 350 ₽ · еда · сегодня", "отложенная трата записана")

	actor, err := fams.Actor(ctx, 7)
	require.NoError(t, err)
	items, _, _, err := exp.List(ctx, domain.ExpenseFilter{Scope: domain.ScopeOf(actor)})
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

func TestInviteCodeIsNotMistakenForExpense(t *testing.T) {
	ctx := context.Background()
	s, tg, b, sender := newBroadcaster(t)
	fams := service.NewFamilies(s, service.FamiliesConfig{MaxMembers: 10, InviteTTL: time.Hour, InviteMaxUses: 10, DefaultTZ: "Europe/Moscow"})
	users(t, s, 1)
	_, inv, err := fams.CreateFamily(ctx, 1, "Ивановы")
	require.NoError(t, err)
	exp := service.NewExpenses(s, "Europe/Moscow")
	dict, _ := lexicon.LoadDictionary("../../config/synonyms.yaml")
	cat := category.NewCategorizer(dict, nil, nil, time.Second)
	h := NewHandler(HandlerDeps{
		Store: s, Families: fams, Sender: sender, API: NewAPI("http://127.0.0.1:9", "x"), Broadcast: b,
		Agent:   agent.New(llm.NewFake(), exp, dict, cat, filter.NewAnalyzer(dict, cat, 300), nil, agent.Config{}),
		FSM:     onboarding.New(s, fams, "Europe/Moscow", time.Hour, func() string { return "bot" }),
		Limiter: limits.NewRateLimiter(100, time.Minute), BotUsername: func() string { return "bot" },
	})
	require.NoError(t, h.message(ctx, &Message{From: &User{ID: 8}, Chat: Chat{ID: 8, Type: "private"}, Text: service.FormatInviteCode(inv.Code)}))
	assert.Contains(t, tg.messages(8)[0].Text, "присоединились", "код приглашения - это вступление, а не трата")
}

func TestLeaveAndDeleteConfirmedByText(t *testing.T) {
	ctx := context.Background()
	s, tg, b, sender := newBroadcaster(t)
	fams := service.NewFamilies(s, service.FamiliesConfig{MaxMembers: 10, InviteTTL: time.Hour, InviteMaxUses: 10, DefaultTZ: "UTC"})
	users(t, s, 1, 2)
	_, inv, err := fams.CreateFamily(ctx, 1, "Ивановы")
	require.NoError(t, err)
	_, err = fams.JoinByCode(ctx, 2, inv.Code)
	require.NoError(t, err)
	for _, id := range []int64{1, 2} {
		require.NoError(t, s.SetUserState(ctx, id, domain.StateActive, nil))
	}
	exp := service.NewExpenses(s, "UTC")
	dict, _ := lexicon.LoadDictionary("../../config/synonyms.yaml")
	cat := category.NewCategorizer(dict, nil, nil, time.Second)
	h := NewHandler(HandlerDeps{
		Store: s, Families: fams, Sender: sender, API: NewAPI("http://127.0.0.1:9", "x"), Broadcast: b,
		Agent:   agent.New(llm.NewFake(), exp, dict, cat, filter.NewAnalyzer(dict, cat, 300), nil, agent.Config{}),
		FSM:     onboarding.New(s, fams, "UTC", time.Hour, func() string { return "bot" }),
		Limiter: limits.NewRateLimiter(100, time.Minute), BotUsername: func() string { return "bot" },
	})
	say := func(id int64, text string) string {
		t.Helper()
		before := len(tg.messages(id))
		require.NoError(t, h.message(ctx, &Message{From: &User{ID: id}, Chat: Chat{ID: id, Type: "private"}, Text: text}))
		out := ""
		for _, m := range tg.messages(id)[before:] {
			out += m.Text + "\n"
		}
		return out
	}

	assert.Contains(t, say(2, "выйти из семьи"), "Ответьте «да» или «нет»")
	assert.Contains(t, say(2, "нет"), "ничего не меняю")
	_, err = fams.Actor(ctx, 2)
	require.NoError(t, err, "после «нет» участник в семье")

	say(2, "выйти из семьи")
	assert.Contains(t, say(2, "да"), "Вы вышли из семьи")
	_, err = fams.Actor(ctx, 2)
	assert.ErrorIs(t, err, domain.ErrNotInFamily)

	// Другое сообщение отменяет ожидание: «да» после него данные не удаляет.
	assert.Contains(t, say(1, "удалить мои данные"), "Ответьте «да» или «нет»")
	say(1, "кофе 100")
	assert.NotContains(t, say(1, "да"), "удалены")
	_, err = fams.Actor(ctx, 1)
	require.NoError(t, err, "данные владельца на месте")
}
