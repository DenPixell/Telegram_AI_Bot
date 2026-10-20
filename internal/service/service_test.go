package service_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
	"expense-bot/internal/service"
	"expense-bot/internal/storage/postgres"
	"expense-bot/internal/testutil/pgtest"
	"expense-bot/internal/testutil/testcats"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

var ctx = context.Background()

func setup(t *testing.T, maxMembers int) (*postgres.Store, *service.Families) {
	t.Helper()
	s, _ := pgtest.New(t)
	return s, service.NewFamilies(s, service.FamiliesConfig{
		MaxMembers: maxMembers, InviteTTL: time.Hour, InviteMaxUses: 10, DefaultTZ: "Europe/Moscow",
	})
}

func users(t *testing.T, s *postgres.Store, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		_, err := s.UpsertUser(ctx, id, "u", "")
		require.NoError(t, err)
	}
}

// ownerWithFamily: пользователь 1 создаёт семью «Ивановы».
func ownerWithFamily(t *testing.T, s *postgres.Store, f *service.Families) (domain.Actor, domain.Invite) {
	t.Helper()
	users(t, s, 1)
	_, inv, err := f.CreateFamily(ctx, 1, "Ивановы")
	require.NoError(t, err)
	a, err := f.Actor(ctx, 1)
	require.NoError(t, err)
	return a, inv
}

func TestCreateFamily(t *testing.T) {
	s, f := setup(t, 10)
	users(t, s, 1)
	require.NoError(t, s.SetUserTimezone(ctx, 1, "Asia/Novosibirsk"))

	fam, inv, err := f.CreateFamily(ctx, 1, "  Ивановы  ")
	require.NoError(t, err)
	assert.Equal(t, "Ивановы", fam.Name)
	assert.Equal(t, "Asia/Novosibirsk", fam.DefaultTZ, "пояс семьи - пояс создателя")
	assert.Len(t, inv.Code, service.InviteCodeLen, "первое приглашение создаётся сразу")

	a, err := f.Actor(ctx, 1)
	require.NoError(t, err)
	assert.True(t, a.IsOwner())

	_, _, err = f.CreateFamily(ctx, 1, "Ещё одна")
	assert.ErrorIs(t, err, domain.ErrInFamily)

	_, err = f.Actor(ctx, 99)
	assert.ErrorIs(t, err, domain.ErrNotInFamily)
}

func TestJoinByCode(t *testing.T) {
	s, f := setup(t, 10)
	_, inv := ownerWithFamily(t, s, f)
	users(t, s, 2)

	fam, err := f.JoinByCode(ctx, 2, service.FormatInviteCode(inv.Code))
	require.NoError(t, err)
	assert.Equal(t, "Ивановы", fam.Name)
	a, err := f.Actor(ctx, 2)
	require.NoError(t, err)
	assert.Equal(t, domain.RoleMember, a.Role)

	_, err = f.JoinByCode(ctx, 2, inv.Code)
	assert.ErrorIs(t, err, domain.ErrInFamily, "сначала нужно выйти из семьи")
}

func TestJoinRespectsMemberLimitUnderConcurrency(t *testing.T) {
	s, f := setup(t, 3)
	_, inv := ownerWithFamily(t, s, f)
	ids := []int64{10, 11, 12, 13, 14}
	users(t, s, ids...)

	var ok, full atomic.Int32
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.JoinByCode(ctx, id, inv.Code)
			switch {
			case err == nil:
				ok.Add(1)
			case assert.ErrorIs(t, err, domain.ErrFamilyFull):
				full.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 2, ok.Load(), "владелец + 2 = лимит 3")
	assert.EqualValues(t, 3, full.Load())

	owner, _ := f.Actor(ctx, 1)
	invs, err := f.ListInvites(ctx, owner)
	require.NoError(t, err)
	assert.Equal(t, 2, invs[0].UsedCount, "откат возвращает использование приглашения")
}

func TestJoinAttemptsLimit(t *testing.T) {
	s, f := setup(t, 10)
	_, inv := ownerWithFamily(t, s, f)
	users(t, s, 2)
	for i := range 5 {
		_, err := f.JoinByCode(ctx, 2, "ZZZZZZZZZ"+string(rune('0'+i)))
		assert.ErrorIs(t, err, domain.ErrInviteInvalid)
	}
	_, err := f.JoinByCode(ctx, 2, inv.Code)
	assert.ErrorIs(t, err, domain.ErrTooManyAttempts, "после 5 ошибок блокируется даже верный код")
}

func TestOwnerOnlyOperations(t *testing.T) {
	s, f := setup(t, 10)
	_, inv := ownerWithFamily(t, s, f)
	users(t, s, 2)
	_, err := f.JoinByCode(ctx, 2, inv.Code)
	require.NoError(t, err)
	member, _ := f.Actor(ctx, 2)

	_, err = f.CreateInvite(ctx, member)
	assert.ErrorIs(t, err, domain.ErrForbidden)
	_, err = f.ListMembers(ctx, member)
	assert.ErrorIs(t, err, domain.ErrForbidden)
	assert.ErrorIs(t, f.RemoveMember(ctx, member, 1), domain.ErrForbidden)
	_, err = f.UpdateFamily(ctx, member, "x", "UTC")
	assert.ErrorIs(t, err, domain.ErrForbidden)
}

func TestCrossFamilyAccessDenied(t *testing.T) {
	s, f := setup(t, 10)
	_, inv := ownerWithFamily(t, s, f)
	users(t, s, 2, 3)
	_, err := f.JoinByCode(ctx, 2, inv.Code)
	require.NoError(t, err)
	_, _, err = f.CreateFamily(ctx, 3, "Петровы")
	require.NoError(t, err)
	other, _ := f.Actor(ctx, 3)

	assert.ErrorIs(t, f.SetRelation(ctx, other, 2, "муж"), domain.ErrNotFound)
	assert.ErrorIs(t, f.RemoveMember(ctx, other, 2), domain.ErrNotFound)
	assert.ErrorIs(t, f.TransferOwnership(ctx, other, 2), domain.ErrNotFound)
}

func TestTransferOwnershipAndLeave(t *testing.T) {
	s, f := setup(t, 10)
	owner, inv := ownerWithFamily(t, s, f)
	users(t, s, 2)
	_, err := f.JoinByCode(ctx, 2, inv.Code)
	require.NoError(t, err)

	_, err = f.Leave(ctx, owner)
	assert.ErrorIs(t, err, domain.ErrOwnerMustStay)
	assert.ErrorIs(t, f.RemoveMember(ctx, owner, 1), domain.ErrValidation, "себя удалить нельзя")

	require.NoError(t, f.TransferOwnership(ctx, owner, 2))
	newOwner, _ := f.Actor(ctx, 2)
	assert.True(t, newOwner.IsOwner())
	assert.Error(t, f.TransferOwnership(ctx, owner, 1), "устаревший Actor бывшего владельца")

	deleted, err := f.Leave(ctx, owner) // устаревший Actor: на деле уже участник
	require.NoError(t, err)
	assert.False(t, deleted)
	u, _ := s.GetUser(ctx, 1)
	assert.Equal(t, domain.StateAwaitingChoice, u.State, "после выхода - снова выбор семьи")

	deleted, err = f.Leave(ctx, newOwner)
	require.NoError(t, err)
	assert.True(t, deleted, "последний участник удаляет семью")
}

func TestDeleteMyData(t *testing.T) {
	s, f := setup(t, 10)
	_, inv := ownerWithFamily(t, s, f)
	users(t, s, 2)
	_, err := f.JoinByCode(ctx, 2, inv.Code)
	require.NoError(t, err)

	_, err = f.DeleteMyData(ctx, 1)
	assert.ErrorIs(t, err, domain.ErrOwnerMustStay)

	deleted, err := f.DeleteMyData(ctx, 2)
	require.NoError(t, err)
	assert.False(t, deleted)
	_, err = s.GetUser(ctx, 2)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestExpensesValidation(t *testing.T) {
	s, f := setup(t, 10)
	owner, _ := ownerWithFamily(t, s, f)
	exp := service.NewExpenses(s, testcats.Set(), "Europe/Moscow")
	today, err := exp.Today(ctx, 1)
	require.NoError(t, err)

	cases := []struct {
		name string
		e    domain.Expense
		ok   bool
	}{
		{"ok", domain.Expense{Amount: 35000, Category: testcats.Food}, true},
		{"zero amount", domain.Expense{Amount: 0, Category: testcats.Food}, false},
		{"unknown category", domain.Expense{Amount: 1, Category: "крипта"}, false},
		{"tomorrow is allowed", domain.Expense{Amount: 1, Category: testcats.Food, SpentAt: today.AddDays(1)}, true},
		{"day after tomorrow", domain.Expense{Amount: 1, Category: testcats.Food, SpentAt: today.AddDays(2)}, false},
		{"six years ago", domain.Expense{Amount: 1, Category: testcats.Food, SpentAt: today.AddDays(-6 * 366)}, false},
		{"foreign user", domain.Expense{UserID: 999, Amount: 1, Category: testcats.Food}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := exp.Create(ctx, owner, c.e)
			if c.ok {
				require.NoError(t, err)
				assert.Equal(t, owner.FamilyID, res.FamilyID, "family_id берётся из Actor")
				return
			}
			assert.ErrorIs(t, err, domain.ErrValidation)
		})
	}

	long, err := exp.Create(ctx, owner, domain.Expense{Amount: 1, Category: testcats.Other, Comment: strings.Repeat("я", 300)})
	require.NoError(t, err)
	assert.Len(t, []rune(long.Comment), domain.MaxCommentLen, "комментарий обрезается, а не отвергается")

	// NUL-символ (JSON \u0000 из панели) не должен превращаться в 500 от PostgreSQL.
	nul, err := exp.Create(ctx, owner, domain.Expense{Amount: 1, Category: testcats.Other, Title: "ко\x00фе", Comment: "\x00"})
	require.NoError(t, err)
	assert.Equal(t, "кофе", nul.Title)
}

func TestAuthMagicLink(t *testing.T) {
	s, f := setup(t, 10)
	owner, inv := ownerWithFamily(t, s, f)
	users(t, s, 2)
	_, err := f.JoinByCode(ctx, 2, inv.Code)
	require.NoError(t, err)
	member, _ := f.Actor(ctx, 2)
	auth := service.NewAuth(s)

	_, _, err = auth.CreateLoginToken(ctx, member)
	assert.ErrorIs(t, err, domain.ErrForbidden, "панель - только владельцу")

	token, _, err := auth.CreateLoginToken(ctx, owner)
	require.NoError(t, err)
	sess, err := auth.Exchange(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, owner.UserID, sess.Actor.UserID)
	_, err = auth.Exchange(ctx, token)
	assert.ErrorIs(t, err, service.ErrUnauthenticated, "ссылка одноразовая")

	a, err := auth.SessionActor(ctx, sess.ID)
	require.NoError(t, err)
	assert.True(t, a.IsOwner())
	assert.NotEqual(t, service.CSRFToken(sess.ID), service.CSRFToken("other"))

	// После передачи владения сессия бывшего владельца больше не работает.
	require.NoError(t, f.TransferOwnership(ctx, owner, 2))
	_, err = auth.SessionActor(ctx, sess.ID)
	assert.ErrorIs(t, err, service.ErrUnauthenticated)
}
