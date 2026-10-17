package onboarding_test

import (
	"context"
	"regexp"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
	"expense-bot/internal/onboarding"
	"expense-bot/internal/service"
	"expense-bot/internal/storage/postgres"
	"expense-bot/internal/testutil/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

var ctx = context.Background()

type flow struct {
	t     *testing.T
	store *postgres.Store
	fams  *service.Families
	fsm   *onboarding.FSM
}

func newFlow(t *testing.T) *flow {
	t.Helper()
	s, _ := pgtest.New(t)
	fams := service.NewFamilies(s, service.FamiliesConfig{MaxMembers: 10, InviteTTL: 168 * time.Hour, InviteMaxUses: 10, DefaultTZ: "Europe/Moscow"})
	return &flow{t: t, store: s, fams: fams,
		fsm: onboarding.New(s, fams, "Europe/Moscow", 168*time.Hour, func() string { return "test_bot" })}
}

// user перечитывает пользователя: FSM хранит состояние в БД, как в боте.
func (f *flow) user(id int64) domain.User {
	f.t.Helper()
	u, err := f.store.UpsertUser(ctx, id, "u", "")
	require.NoError(f.t, err)
	return u
}

func (f *flow) text(id int64, text string) onboarding.Reply {
	f.t.Helper()
	r, err := f.fsm.HandleText(ctx, f.user(id), text)
	require.NoError(f.t, err)
	return r
}

func (f *flow) start(id int64, payload string) onboarding.Reply {
	f.t.Helper()
	r, err := f.fsm.Start(ctx, f.user(id), payload)
	require.NoError(f.t, err)
	return r
}

func (f *flow) state(id int64) domain.UserState { return f.user(id).State }

var codeRe = regexp.MustCompile(`start=([0-9A-Z]{10})`)

// createFamily проводит владельца через весь онбординг и возвращает код приглашения.
func (f *flow) createFamily(id int64, name, tz string) string {
	f.t.Helper()
	f.start(id, "")
	f.text(id, "создать")
	r := f.text(id, name)
	m := codeRe.FindStringSubmatch(r.Text)
	require.NotNil(f.t, m, r.Text)
	f.text(id, tz)
	require.Equal(f.t, domain.StateActive, f.state(id))
	return m[1]
}

func TestCreateFamilyFlow(t *testing.T) {
	f := newFlow(t)
	r := f.start(1, "")
	assert.Contains(t, r.Text, "создайте семью")
	require.NotEmpty(t, r.Buttons, "кнопки «Создать семью» и «У меня есть код»")
	assert.Equal(t, domain.StateAwaitingChoice, f.state(1))

	r = f.text(1, "Создать")
	assert.Contains(t, r.Text, "Как назовём семью")
	assert.Equal(t, domain.StateAwaitingFamilyName, f.state(1))

	r = f.text(1, "Ивановы")
	assert.Contains(t, r.Text, "Семья «Ивановы» создана")
	assert.Contains(t, r.Text, "https://t.me/test_bot?start=")
	assert.True(t, r.RequestLocation, "кнопка геолокации на шаге пояса")
	assert.Equal(t, domain.StateAwaitingTZ, f.state(1))

	// Пока регистрация не закончена, трата не записывается - FSM переспрашивает пояс.
	r = f.text(1, "кофе 350")
	assert.Contains(t, r.Text, "Не узнал такой часовой пояс")

	r = f.text(1, "Новосибирск")
	assert.Contains(t, r.Text, "Готово! Часовой пояс: Новосибирск (UTC+7)")
	assert.Contains(t, r.Text, "Для главы семьи", "владельцу - его команды")
	assert.True(t, r.RemoveKeyboard)
	assert.Equal(t, domain.StateActive, f.state(1))

	a, err := f.fams.Actor(ctx, 1)
	require.NoError(t, err)
	fam, err := f.fams.GetFamily(ctx, a)
	require.NoError(t, err)
	assert.Equal(t, "Asia/Novosibirsk", fam.DefaultTZ, "пояс единственного участника становится поясом семьи")
}

func TestJoinByDeepLink(t *testing.T) {
	f := newFlow(t)
	code := f.createFamily(1, "Ивановы", "Москва")

	r := f.start(2, code)
	assert.Contains(t, r.Text, "присоединились к семье «Ивановы»")
	assert.Equal(t, domain.StateAwaitingTZ, f.state(2))

	r, err := f.fsm.HandleLocation(ctx, f.user(2), 55.03, 82.92)
	require.NoError(t, err)
	assert.Contains(t, r.Text, "Новосибирск (UTC+7)")
	assert.NotContains(t, r.Text, "Для главы семьи", "участнику - без команд владельца")

	r = f.start(2, code)
	assert.Contains(t, r.Text, "уже состоите в семье")
}

func TestJoinByManualCode(t *testing.T) {
	f := newFlow(t)
	code := f.createFamily(1, "Ивановы", "Москва")
	f.start(2, "")
	r := f.text(2, "у меня есть код")
	assert.Contains(t, r.Text, "Пришлите код")
	r = f.text(2, code[:5]+"-"+code[5:])
	assert.Contains(t, r.Text, "присоединились")
}

func TestInvalidCodeAndAttemptsLimit(t *testing.T) {
	f := newFlow(t)
	code := f.createFamily(1, "Ивановы", "Москва")
	r := f.start(2, "ZZZZZZZZZZ")
	assert.Contains(t, r.Text, "Код не найден или больше не действует")
	assert.NotEmpty(t, r.Buttons)
	for range 4 {
		f.text(2, "ZZZZZZZZZZ")
	}
	r = f.start(2, code)
	assert.Contains(t, r.Text, "Слишком много неудачных попыток")
}

func TestTimezoneRetryThenFamilyDefault(t *testing.T) {
	f := newFlow(t)
	f.start(1, "")
	f.text(1, "создать")
	f.text(1, "пропустить") // семья без названия
	r := f.text(1, "абракадабра")
	assert.Contains(t, r.Text, "Не узнал")
	assert.Equal(t, domain.StateAwaitingTZ, f.state(1))
	r = f.text(1, "абракадабра")
	assert.Contains(t, r.Text, "поставил часовой пояс семьи")
	assert.Equal(t, domain.StateActive, f.state(1), "переспрашиваем один раз")
}

func TestCallbacks(t *testing.T) {
	f := newFlow(t)
	f.start(1, "")
	r, err := f.fsm.Callback(ctx, f.user(1), onboarding.CbCreate)
	require.NoError(t, err)
	assert.Contains(t, r.Text, "Как назовём семью")
	r, err = f.fsm.Callback(ctx, f.user(1), onboarding.CbSkip)
	require.NoError(t, err)
	assert.Contains(t, r.Text, "Семья «без названия» создана")
}

func TestStartForActiveUser(t *testing.T) {
	f := newFlow(t)
	f.createFamily(1, "Ивановы", "UTC+5")
	r := f.start(1, "")
	assert.Contains(t, r.Text, "С возвращением")
}

func TestPendingExpenseSurvivesOnboarding(t *testing.T) {
	f := newFlow(t)
	r, err := f.fsm.Remember(ctx, f.user(1), "кофе 350")
	require.NoError(t, err)
	assert.Contains(t, r.Text, "Запишу «кофе 350»")
	assert.NotEmpty(t, r.Buttons, "сразу выбор: создать семью или ввести код")
	assert.Equal(t, domain.StateAwaitingChoice, f.state(1))

	f.text(1, "создать")
	f.text(1, "Ивановы")
	assert.Contains(t, f.text(1, "абракадабра").Text, "Не узнал", "неудачный пояс не теряет трату")
	r = f.text(1, "Москва")
	assert.Equal(t, "кофе 350", r.Pending, "после регистрации трату надо записать")
	assert.JSONEq(t, `{}`, string(f.user(1).StateData), "после записи отложенная трата не хранится")
}

func TestPendingExpenseAfterJoin(t *testing.T) {
	f := newFlow(t)
	code := f.createFamily(1, "Ивановы", "Москва")
	_, err := f.fsm.Remember(ctx, f.user(2), "такси 1200 вчера")
	require.NoError(t, err)
	f.start(2, code)
	r, err := f.fsm.HandleLocation(ctx, f.user(2), 55.75, 37.6)
	require.NoError(t, err)
	assert.Equal(t, "такси 1200 вчера", r.Pending)
}
