package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchIntent(t *testing.T) {
	cases := []struct {
		text string
		kind IntentKind
		arg  string
	}{
		{"помощь", IntentHelp, ""},
		{"/help", IntentHelp, ""},
		{"Панель!", IntentPanel, ""},
		{"/panel@MyFamilyBudgetplus_Bot", IntentPanel, ""},
		{"ссылка", IntentInvite, ""},
		{"выйти из семьи", IntentLeave, ""},
		{"удалить мои данные", IntentDeleteData, ""},
		{"/tz", IntentTimezone, ""},
		{"мой часовой пояс Новосибирск", IntentTimezone, "Новосибирск"},
		{"/tz@bot Asia/Omsk", IntentTimezone, "Asia/Omsk"}, // регистр аргумента сохраняется
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			in, ok := MatchIntent(c.text)
			require.True(t, ok)
			assert.Equal(t, c.kind, in.Kind)
			assert.Equal(t, c.arg, in.Arg)
		})
	}
	for _, text := range []string{"кофе 350", "пояс 500", "панелька 300", "покажи траты"} {
		_, ok := MatchIntent(text)
		assert.False(t, ok, text)
	}
}

func TestRateLimiter(t *testing.T) {
	l := NewRateLimiter(3, time.Minute)
	now := time.Now()
	for i := range 3 {
		assert.True(t, l.Allow(1, now.Add(time.Duration(i)*time.Second)))
	}
	assert.False(t, l.Allow(1, now.Add(5*time.Second)), "четвёртое в окне - нельзя")
	assert.True(t, l.Allow(2, now.Add(5*time.Second)), "лимит у каждого свой")
	assert.True(t, l.Allow(1, now.Add(61*time.Second)), "окно сдвинулось")
}

type fakeCounter struct {
	user, family, total int
	err                 error
}

func (f fakeCounter) CountLLMUsage(context.Context, int64, int64, time.Time) (int, int, int, error) {
	return f.user, f.family, f.total, f.err
}

func TestQuota(t *testing.T) {
	ctx := context.Background()
	assert.NoError(t, NewQuota(fakeCounter{user: 99, family: 399, total: 4999}, 100, 400, 5000).Check(ctx, 1, 1))
	assert.ErrorIs(t, NewQuota(fakeCounter{user: 100}, 100, 400, 5000).Check(ctx, 1, 1), ErrQuotaExceeded)
	assert.ErrorIs(t, NewQuota(fakeCounter{family: 400}, 100, 400, 5000).Check(ctx, 1, 1), ErrQuotaExceeded)
	assert.ErrorIs(t, NewQuota(fakeCounter{total: 5000}, 100, 400, 5000).Check(ctx, 1, 1), ErrQuotaExceeded)
	assert.NoError(t, NewQuota(fakeCounter{total: 1e6}, 100, 400, 0).Check(ctx, 1, 1), "0 - без глобального лимита")

	err := NewQuota(fakeCounter{err: errors.New("db down")}, 100, 400, 0).Check(ctx, 1, 1)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrQuotaExceeded, "сбой БД - не «квота исчерпана»")
}
