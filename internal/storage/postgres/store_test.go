package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
	"expense-bot/internal/llm"
	"expense-bot/internal/storage/postgres"
	"expense-bot/internal/testutil/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

var ctx = context.Background()

func d(s string) domain.Date {
	v, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return v
}

// family создаёт семью с владельцем ownerID и участниками memberIDs.
func family(t *testing.T, s *postgres.Store, name string, ownerID int64, memberIDs ...int64) domain.Family {
	t.Helper()
	f, err := s.CreateFamily(ctx, name, "Europe/Moscow")
	require.NoError(t, err)
	for i, id := range append([]int64{ownerID}, memberIDs...) {
		_, err := s.UpsertUser(ctx, id, name+" user", "")
		require.NoError(t, err)
		role := domain.RoleMember
		if i == 0 {
			role = domain.RoleOwner
		}
		_, err = s.AddMember(ctx, domain.Member{UserID: id, FamilyID: f.ID, Role: role})
		require.NoError(t, err)
	}
	return f
}

func expense(t *testing.T, s *postgres.Store, familyID, userID int64, rub int64, cat domain.Category, date, title string) domain.Expense {
	t.Helper()
	e, err := s.CreateExpense(ctx, domain.Expense{
		FamilyID: familyID, UserID: userID, Amount: domain.Money(rub * 100), Category: cat, SpentAt: d(date), Title: title, Comment: title,
	})
	require.NoError(t, err)
	return e
}

func TestUsers(t *testing.T) {
	s, _ := pgtest.New(t)
	u, err := s.UpsertUser(ctx, 7_000_000_001, "Аня", "anya") // Telegram ID больше 2^31
	require.NoError(t, err)
	assert.Equal(t, domain.StateNew, u.State)
	assert.JSONEq(t, `{}`, string(u.StateData))

	require.NoError(t, s.SetUserState(ctx, u.TelegramID, domain.StateAwaitingTZ, json.RawMessage(`{"tz_attempts":1}`)))
	require.NoError(t, s.SetUserTimezone(ctx, u.TelegramID, "Asia/Novosibirsk"))
	u, err = s.GetUser(ctx, u.TelegramID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateAwaitingTZ, u.State)
	assert.Equal(t, "Asia/Novosibirsk", u.Timezone)

	// Повторный upsert обновляет имя, но не трогает состояние и пояс.
	u, err = s.UpsertUser(ctx, u.TelegramID, "Анна", "anna")
	require.NoError(t, err)
	assert.Equal(t, "Анна", u.DisplayName)
	assert.Equal(t, domain.StateAwaitingTZ, u.State)

	_, err = s.GetUser(ctx, 42)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.ErrorIs(t, s.SetUserActive(ctx, 42, false), domain.ErrNotFound)
}

func TestMembershipConstraints(t *testing.T) {
	s, _ := pgtest.New(t)
	f1 := family(t, s, "Ивановы", 1, 2)
	f2 := family(t, s, "Петровы", 3)

	_, err := s.AddMember(ctx, domain.Member{UserID: 2, FamilyID: f2.ID, Role: domain.RoleMember})
	assert.ErrorIs(t, err, domain.ErrInFamily, "одна семья на пользователя")

	_, err = s.UpsertUser(ctx, 4, "x", "")
	require.NoError(t, err)
	_, err = s.AddMember(ctx, domain.Member{UserID: 4, FamilyID: f1.ID, Role: domain.RoleOwner})
	assert.ErrorIs(t, err, domain.ErrConflict, "ровно один владелец")

	ms, err := s.ListMembers(ctx, f1.ID)
	require.NoError(t, err)
	require.Len(t, ms, 2)
	assert.Equal(t, domain.RoleOwner, ms[0].Role, "владелец первым")

	// Чужая семья не видна и не меняется.
	assert.ErrorIs(t, s.SetMemberRelation(ctx, f2.ID, 2, "муж"), domain.ErrNotFound)
	assert.ErrorIs(t, s.RemoveMember(ctx, f2.ID, 2), domain.ErrNotFound)

	_, err = s.SoleFamilyID(ctx)
	assert.ErrorIs(t, err, domain.ErrConflict)
}

func TestTransactions(t *testing.T) {
	s, pool := pgtest.New(t)
	err := s.WithTx(ctx, func(ctx context.Context) error {
		_, err := s.CreateFamily(ctx, "rollback", "UTC")
		require.NoError(t, err)
		return errors.New("boom")
	})
	require.Error(t, err)
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM families`).Scan(&n))
	assert.Zero(t, n, "откат транзакции")

	assert.Error(t, s.LockUser(ctx, 1), "блокировка вне транзакции бессмысленна")
	assert.NoError(t, s.WithTx(ctx, func(ctx context.Context) error { return s.LockUser(ctx, 1) }))

	// Вложенная транзакция - savepoint: её откат не трогает внешнюю.
	require.NoError(t, s.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.CreateFamily(ctx, "outer", "UTC"); err != nil {
			return err
		}
		_ = s.WithTx(ctx, func(ctx context.Context) error {
			_, _ = s.CreateFamily(ctx, "inner", "UTC")
			return errors.New("inner boom")
		})
		return nil
	}))
	var names []string
	rows, err := pool.Query(ctx, `SELECT name FROM families ORDER BY id`)
	require.NoError(t, err)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	assert.Equal(t, []string{"outer"}, names)
}

func TestInviteUsedConcurrently(t *testing.T) {
	s, _ := pgtest.New(t)
	f := family(t, s, "Ивановы", 1)
	_, err := s.CreateInvite(ctx, domain.Invite{FamilyID: f.ID, Code: "AAAAAAAAAA", CreatedBy: 1, ExpiresAt: time.Now().Add(time.Hour), MaxUses: 3})
	require.NoError(t, err)

	var okCount atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.UseInvite(ctx, "AAAAAAAAAA")
			if err == nil {
				okCount.Add(1)
			} else {
				assert.ErrorIs(t, err, domain.ErrInviteInvalid)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 3, okCount.Load(), "ровно max_uses успешных использований")
}

func TestInviteValidity(t *testing.T) {
	s, _ := pgtest.New(t)
	f := family(t, s, "Ивановы", 1)
	other := family(t, s, "Петровы", 2)
	_, err := s.CreateInvite(ctx, domain.Invite{FamilyID: f.ID, Code: "BBBBBBBBBB", CreatedBy: 1, ExpiresAt: time.Now().Add(time.Hour), MaxUses: 5})
	require.NoError(t, err)
	_, err = s.CreateInvite(ctx, domain.Invite{FamilyID: f.ID, Code: "BBBBBBBBBB", CreatedBy: 1, ExpiresAt: time.Now().Add(time.Hour), MaxUses: 5})
	assert.ErrorIs(t, err, domain.ErrDuplicateInviteCode)

	expired, err := s.CreateInvite(ctx, domain.Invite{FamilyID: f.ID, Code: "CCCCCCCCCC", CreatedBy: 1, ExpiresAt: time.Now().Add(-time.Minute), MaxUses: 5})
	require.NoError(t, err)
	_, err = s.UseInvite(ctx, expired.Code)
	assert.ErrorIs(t, err, domain.ErrInviteInvalid)
	_, err = s.UseInvite(ctx, "ZZZZZZZZZZ")
	assert.ErrorIs(t, err, domain.ErrInviteInvalid)

	active, err := s.ListActiveInvites(ctx, f.ID)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.ErrorIs(t, s.RevokeInvite(ctx, other.ID, active[0].ID), domain.ErrNotFound, "чужую не отозвать")
	require.NoError(t, s.RevokeInvite(ctx, f.ID, active[0].ID))
	_, err = s.UseInvite(ctx, "BBBBBBBBBB")
	assert.ErrorIs(t, err, domain.ErrInviteInvalid)
}

func TestExpenseIsolation(t *testing.T) {
	s, _ := pgtest.New(t)
	f1 := family(t, s, "Ивановы", 1, 2)
	f2 := family(t, s, "Петровы", 3)
	a := expense(t, s, f1.ID, 1, 350, domain.CategoryFood, "2026-10-15", "кофе")
	expense(t, s, f1.ID, 2, 1200, domain.CategoryTransport, "2026-10-16", "такси")
	foreign := expense(t, s, f2.ID, 3, 999, domain.CategoryFood, "2026-10-16", "ресторан")

	user1 := domain.UserScope(f1.ID, 1)
	fam1 := domain.FamilyScope(f1.ID)

	list, err := s.ListExpenses(ctx, domain.ExpenseFilter{Scope: user1})
	require.NoError(t, err)
	assert.Len(t, list, 1, "бот видит только свои траты")

	list, err = s.ListExpenses(ctx, domain.ExpenseFilter{Scope: fam1})
	require.NoError(t, err)
	assert.Len(t, list, 2, "владелец видит семью, но не чужую семью")

	for name, op := range map[string]func() error{
		"get other user":   func() error { _, err := s.GetExpense(ctx, domain.UserScope(f1.ID, 2), a.ID); return err },
		"get other family": func() error { _, err := s.GetExpense(ctx, fam1, foreign.ID); return err },
		"update other":     func() error { _, err := s.UpdateExpense(ctx, domain.UserScope(f1.ID, 2), a); return err },
		"delete other":     func() error { return s.DeleteExpense(ctx, fam1, foreign.ID) },
	} {
		assert.ErrorIs(t, op(), domain.ErrNotFound, name)
	}

	_, err = s.ListExpenses(ctx, domain.ExpenseFilter{})
	assert.ErrorIs(t, err, domain.ErrForbidden, "нулевая область видимости запрещена")
}

func TestExpenseFilters(t *testing.T) {
	s, _ := pgtest.New(t)
	f := family(t, s, "Ивановы", 1, 2)
	expense(t, s, f.ID, 1, 350, domain.CategoryFood, "2026-10-15", "кофе")
	expense(t, s, f.ID, 1, 7698, domain.CategoryFood, "2026-10-01", "ресторан")
	expense(t, s, f.ID, 2, 1200, domain.CategoryTransport, "2026-10-16", "такси")
	expense(t, s, f.ID, 2, 50, domain.CategoryOther, "2026-09-20", "100% скидка")
	fam := domain.FamilyScope(f.ID)

	count := func(f domain.ExpenseFilter) int {
		t.Helper()
		items, err := s.ListExpenses(ctx, f)
		require.NoError(t, err)
		return len(items)
	}
	assert.Equal(t, 2, count(domain.ExpenseFilter{Scope: fam, MemberID: 2}))
	assert.Equal(t, 2, count(domain.ExpenseFilter{Scope: fam, From: d("2026-10-10"), To: d("2026-10-16")}))
	assert.Equal(t, 2, count(domain.ExpenseFilter{Scope: fam, Categories: []domain.Category{domain.CategoryFood}}))
	assert.Equal(t, 1, count(domain.ExpenseFilter{Scope: fam, Terms: []string{"РЕСТОРАН", "рестик"}}))
	assert.Equal(t, 1, count(domain.ExpenseFilter{Scope: fam, Terms: []string{"%"}}), "спецсимволы LIKE экранируются")
	assert.Equal(t, 1, count(domain.ExpenseFilter{Scope: fam, Limit: 1, Offset: 1}))

	items, err := s.ListExpenses(ctx, domain.ExpenseFilter{Scope: fam})
	require.NoError(t, err)
	assert.Equal(t, "такси", items[0].Title, "новые даты первыми")

	total, n, err := s.SumExpenses(ctx, domain.ExpenseFilter{Scope: fam, Categories: []domain.Category{domain.CategoryFood}})
	require.NoError(t, err)
	assert.Equal(t, domain.Money(804800), total)
	assert.Equal(t, 2, n)

	sum, err := s.SummarizeExpenses(ctx, domain.ExpenseFilter{Scope: fam})
	require.NoError(t, err)
	assert.Equal(t, 4, sum.Count)
	require.NotEmpty(t, sum.ByCategory)
	assert.Equal(t, domain.CategoryFood, sum.ByCategory[0].Category, "по убыванию суммы")
	assert.Len(t, sum.ByMember, 2)
	assert.Len(t, sum.ByDay, 4)
	assert.True(t, sum.ByDay[0].Date.Before(sum.ByDay[3].Date), "дни по возрастанию")

	last, err := s.LastExpense(ctx, domain.UserScope(f.ID, 1))
	require.NoError(t, err)
	assert.Equal(t, "ресторан", last.Title, "последняя - по времени добавления, а не по дате траты")
}

func TestExpenseOptimisticLocking(t *testing.T) {
	s, _ := pgtest.New(t)
	f := family(t, s, "Ивановы", 1)
	e := expense(t, s, f.ID, 1, 350, domain.CategoryFood, "2026-10-15", "кофе")
	scope := domain.UserScope(f.ID, 1)

	e.Amount = 45000
	upd, err := s.UpdateExpense(ctx, scope, e)
	require.NoError(t, err)
	assert.Equal(t, 2, upd.Version)

	_, err = s.UpdateExpense(ctx, scope, e) // версия 1 устарела
	assert.ErrorIs(t, err, domain.ErrConflict)

	_, err = s.CreateExpense(ctx, domain.Expense{FamilyID: f.ID, UserID: 1, Amount: 0, Category: domain.CategoryFood, SpentAt: d("2026-10-15")})
	assert.ErrorIs(t, err, domain.ErrValidation, "CHECK amount > 0")
	_, err = s.CreateExpense(ctx, domain.Expense{FamilyID: f.ID, UserID: 1, Amount: 1, Category: "крипта", SpentAt: d("2026-10-15")})
	assert.ErrorIs(t, err, domain.ErrValidation, "CHECK category")
}

func TestFamilyDeletionCascadesAndMemberRemovalKeepsExpenses(t *testing.T) {
	s, _ := pgtest.New(t)
	f := family(t, s, "Ивановы", 1, 2)
	expense(t, s, f.ID, 2, 500, domain.CategoryFood, "2026-10-15", "обед")

	require.NoError(t, s.RemoveMember(ctx, f.ID, 2))
	items, err := s.ListExpenses(ctx, domain.ExpenseFilter{Scope: domain.FamilyScope(f.ID)})
	require.NoError(t, err)
	assert.Len(t, items, 1, "траты ушедшего остаются в семье")

	require.NoError(t, s.DeleteFamily(ctx, f.ID))
	items, err = s.ListExpenses(ctx, domain.ExpenseFilter{Scope: domain.FamilyScope(f.ID)})
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestAuthTokensAndSessions(t *testing.T) {
	s, _ := pgtest.New(t)
	family(t, s, "Ивановы", 1)
	h := sha256.Sum256([]byte("token"))

	require.NoError(t, s.CreateLoginToken(ctx, h[:], 1, time.Now().Add(time.Minute)))
	uid, err := s.UseLoginToken(ctx, h[:])
	require.NoError(t, err)
	assert.EqualValues(t, 1, uid)
	_, err = s.UseLoginToken(ctx, h[:])
	assert.ErrorIs(t, err, domain.ErrNotFound, "токен одноразовый")

	sid := sha256.Sum256([]byte("session"))
	require.NoError(t, s.CreateSession(ctx, sid[:], 1, time.Now().Add(time.Hour)))
	uid, err = s.SessionUser(ctx, sid[:])
	require.NoError(t, err)
	assert.EqualValues(t, 1, uid)
	require.NoError(t, s.DeleteUserSessions(ctx, 1))
	_, err = s.SessionUser(ctx, sid[:])
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestLearnedTermsAndUsage(t *testing.T) {
	s, _ := pgtest.New(t)
	require.NoError(t, s.SaveLearnedTerm(ctx, domain.LearnedTerm{Stem: "пельмен", Word: "пельмени", Title: "пельмень", Category: domain.CategoryFood, Source: "wiktionary"}))
	require.NoError(t, s.SaveLearnedTerm(ctx, domain.LearnedTerm{Stem: "ден", Word: "день", Title: "день", Source: "wiktionary"}))
	got, err := s.GetLearnedTerm(ctx, "пельмен")
	require.NoError(t, err)
	assert.Equal(t, domain.CategoryFood, got.Category)
	neg, err := s.GetLearnedTerm(ctx, "ден")
	require.NoError(t, err)
	assert.Empty(t, neg.Category, "отрицательный кэш")

	require.NoError(t, s.RecordUsage(ctx, llm.UsageRecord{Caller: llm.Caller{UserID: 7_000_000_001, FamilyID: 5}, Model: "m", OK: true}))
	require.NoError(t, s.RecordUsage(ctx, llm.UsageRecord{Model: "m", OK: false})) // без пользователя - NULL
	u, f, total, err := s.CountLLMUsage(ctx, 7_000_000_001, 5, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, [3]int{1, 1, 2}, [3]int{u, f, total})
}

func TestProcessedUpdatesDedup(t *testing.T) {
	s, _ := pgtest.New(t)
	first, err := s.MarkUpdateProcessed(ctx, 100)
	require.NoError(t, err)
	again, err := s.MarkUpdateProcessed(ctx, 100)
	require.NoError(t, err)
	assert.True(t, first)
	assert.False(t, again, "повторная доставка")
}

func TestDeleteUserData(t *testing.T) {
	s, _ := pgtest.New(t)
	f := family(t, s, "Ивановы", 1, 2)
	expense(t, s, f.ID, 2, 500, domain.CategoryFood, "2026-10-15", "обед")
	expense(t, s, f.ID, 1, 300, domain.CategoryFood, "2026-10-15", "кофе")
	require.NoError(t, s.WithTx(ctx, func(ctx context.Context) error { return s.DeleteUserData(ctx, 2) }))

	_, err := s.GetUser(ctx, 2)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	items, err := s.ListExpenses(ctx, domain.ExpenseFilter{Scope: domain.FamilyScope(f.ID)})
	require.NoError(t, err)
	assert.Len(t, items, 1, "траты остальных не тронуты")
}

func TestBroadcastLifecycle(t *testing.T) {
	s, _ := pgtest.New(t)
	for _, id := range []int64{1, 2, 3} {
		_, err := s.UpsertUser(ctx, id, "u", "")
		require.NoError(t, err)
	}
	require.NoError(t, s.SetUserActive(ctx, 3, false))
	ids, err := s.ActiveUserIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2}, ids, "заблокировавшие бота не получают рассылку")
	n, err := s.CountActiveUsers(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	_, err = s.CreateBroadcast(ctx, "", 1)
	assert.ErrorIs(t, err, domain.ErrValidation, "пустой текст отвергает CHECK")

	a, err := s.CreateBroadcast(ctx, "Техобслуживание в 22:00", 1)
	require.NoError(t, err)
	assert.Equal(t, domain.BroadcastDraft, a.Status)
	assert.EqualValues(t, 1, a.CreatedBy)
	b, err := s.CreateBroadcast(ctx, "Вторая", 0)
	require.NoError(t, err)
	assert.Zero(t, b.CreatedBy, "через HTTP API - без автора")

	started, err := s.StartBroadcast(ctx, a.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, domain.BroadcastSending, started.Status)
	_, err = s.StartBroadcast(ctx, a.ID, 2)
	assert.ErrorIs(t, err, domain.ErrNotFound, "повторное нажатие «Отправить»")
	_, err = s.StartBroadcast(ctx, b.ID, 2)
	assert.ErrorIs(t, err, domain.ErrConflict, "две рассылки одновременно нельзя")
	assert.ErrorIs(t, s.CancelBroadcast(ctx, a.ID), domain.ErrNotFound, "начатую не отменить")

	require.NoError(t, s.UpdateBroadcastProgress(ctx, a.ID, 1, 0))
	done, err := s.FinishBroadcast(ctx, a.ID, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, domain.BroadcastDone, done.Status)
	assert.NotNil(t, done.FinishedAt)

	_, err = s.StartBroadcast(ctx, b.ID, 2)
	require.NoError(t, err, "после завершения можно следующую")
	closed, err := s.FailStaleBroadcasts(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, closed, "прерванная рестартом рассылка закрывается при старте")
}
