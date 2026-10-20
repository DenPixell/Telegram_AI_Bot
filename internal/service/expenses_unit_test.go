package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
	"expense-bot/internal/service"
)

// memStore - хранилище в памяти: сервис трат зависит от интерфейса, а не от PostgreSQL,
// поэтому правила проверяются без базы. Не нужные тесту методы берутся из встроенного
// nil-интерфейса и при вызове упадут - так тест сразу покажет лишнее обращение к хранилищу.
type memStore struct {
	service.ExpenseStore
	users   map[int64]domain.User
	members map[int64]domain.Member
	created []domain.Expense
}

func (m *memStore) GetUser(_ context.Context, id int64) (domain.User, error) {
	u, ok := m.users[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func (m *memStore) GetMembership(_ context.Context, id int64) (domain.Member, error) {
	mb, ok := m.members[id]
	if !ok {
		return domain.Member{}, domain.ErrNotFound
	}
	return mb, nil
}

func (m *memStore) GetFamily(_ context.Context, id int64) (domain.Family, error) {
	return domain.Family{ID: id, DefaultTZ: "Europe/Moscow"}, nil
}

func (m *memStore) CreateExpense(_ context.Context, e domain.Expense) (domain.Expense, error) {
	e.ID = int64(len(m.created) + 1)
	m.created = append(m.created, e)
	return e, nil
}

func TestCreateExpenseRulesWithoutDatabase(t *testing.T) {
	owner := domain.Actor{UserID: 1, FamilyID: 10, Role: domain.RoleOwner}
	member := domain.Actor{UserID: 2, FamilyID: 10, Role: domain.RoleMember}
	coffee := domain.Expense{Amount: 35000, Category: domain.CategoryFood, Title: "кофе"}

	tests := []struct {
		name     string
		actor    domain.Actor
		expense  func(e domain.Expense) domain.Expense
		wantErr  error
		wantUser int64
	}{
		{"своя трата", member, func(e domain.Expense) domain.Expense { return e }, nil, 2},
		{"владелец за участника семьи", owner, func(e domain.Expense) domain.Expense { e.UserID = 2; return e }, nil, 2},
		{"участник за другого", member, func(e domain.Expense) domain.Expense { e.UserID = 1; return e }, domain.ErrForbidden, 0},
		{"владелец за чужого", owner, func(e domain.Expense) domain.Expense { e.UserID = 3; return e }, domain.ErrValidation, 0},
		{"нулевая сумма", member, func(e domain.Expense) domain.Expense { e.Amount = 0; return e }, domain.ErrValidation, 0},
		{"неизвестная категория", member, func(e domain.Expense) domain.Expense { e.Category = "казино"; return e }, domain.ErrValidation, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &memStore{
				users: map[int64]domain.User{
					1: {TelegramID: 1}, 2: {TelegramID: 2, Timezone: "Asia/Novosibirsk"}, 3: {TelegramID: 3},
				},
				members: map[int64]domain.Member{
					1: {UserID: 1, FamilyID: 10, Role: domain.RoleOwner},
					2: {UserID: 2, FamilyID: 10, Role: domain.RoleMember},
					3: {UserID: 3, FamilyID: 20, Role: domain.RoleOwner},
				},
			}
			svc := service.NewExpenses(st, "UTC")
			got, err := svc.Create(context.Background(), tt.actor, tt.expense(coffee))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Empty(t, st.created, "при ошибке в хранилище ничего не пишется")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantUser, got.UserID)
			assert.Equal(t, int64(10), got.FamilyID, "семья берётся из Actor, а не из траты")
			assert.False(t, got.SpentAt.IsZero(), "пустая дата - сегодня в поясе пользователя")
		})
	}
}

func TestTodayUsesUserTimezone(t *testing.T) {
	st := &memStore{
		users:   map[int64]domain.User{2: {TelegramID: 2, Timezone: "Asia/Novosibirsk"}},
		members: map[int64]domain.Member{},
	}
	svc := service.NewExpenses(st, "UTC")
	today, err := svc.Today(context.Background(), 2)
	require.NoError(t, err)
	loc, err := time.LoadLocation("Asia/Novosibirsk")
	require.NoError(t, err)
	now := time.Now().In(loc)
	assert.Equal(t, now.Year(), today.Year)
	assert.Equal(t, int(now.Month()), int(today.Month))
	assert.Equal(t, now.Day(), today.Day)
}
