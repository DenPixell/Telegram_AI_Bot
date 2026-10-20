package service

import (
	"context"
	"encoding/json"
	"time"

	"expense-bot/internal/domain"
)

// Хранилища, которые нужны сервисам. Интерфейсы объявлены здесь, у потребителя, и содержат
// только вызываемые методы: сервис не знает о PostgreSQL, а в тестах его можно собрать
// на любой реализации. postgres.Store удовлетворяет им всем.

// Transactor выполняет fn в транзакции. Все методы хранилища, вызванные с контекстом,
// переданным в fn, идут в эту транзакцию; вложенный вызов создаёт savepoint.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type membershipReader interface {
	GetMembership(ctx context.Context, userID int64) (domain.Member, error)
}

// ExpenseStore - траты и данные, нужные для «сегодня» в поясе пользователя.
type ExpenseStore interface {
	membershipReader
	GetUser(ctx context.Context, telegramID int64) (domain.User, error)
	GetFamily(ctx context.Context, id int64) (domain.Family, error)

	CreateExpense(ctx context.Context, e domain.Expense) (domain.Expense, error)
	GetExpense(ctx context.Context, scope domain.ExpenseScope, id int64) (domain.Expense, error)
	UpdateExpense(ctx context.Context, scope domain.ExpenseScope, e domain.Expense) (domain.Expense, error)
	DeleteExpense(ctx context.Context, scope domain.ExpenseScope, id int64) error
	LastExpense(ctx context.Context, scope domain.ExpenseScope) (domain.Expense, error)
	ListExpenses(ctx context.Context, f domain.ExpenseFilter) ([]domain.Expense, error)
	SumExpenses(ctx context.Context, f domain.ExpenseFilter) (domain.Money, int, error)
	SummarizeExpenses(ctx context.Context, f domain.ExpenseFilter) (domain.ExpenseSummary, error)
}

// FamilyStore - семьи, участники и приглашения.
type FamilyStore interface {
	Transactor
	membershipReader
	GetUser(ctx context.Context, telegramID int64) (domain.User, error)
	SetUserState(ctx context.Context, telegramID int64, state domain.UserState, data json.RawMessage) error
	LockUser(ctx context.Context, userID int64) error
	DeleteUserData(ctx context.Context, userID int64) error
	DeleteUserSessions(ctx context.Context, userID int64) error

	CreateFamily(ctx context.Context, name, defaultTZ string) (domain.Family, error)
	GetFamily(ctx context.Context, id int64) (domain.Family, error)
	UpdateFamily(ctx context.Context, id int64, name, defaultTZ string) (domain.Family, error)
	DeleteFamily(ctx context.Context, id int64) error

	AddMember(ctx context.Context, m domain.Member) (domain.Member, error)
	CountMembers(ctx context.Context, familyID int64) (int, error)
	ListMembers(ctx context.Context, familyID int64) ([]domain.MemberProfile, error)
	SetMemberRelation(ctx context.Context, familyID, userID int64, relation string) error
	SetMemberRole(ctx context.Context, familyID, userID int64, role domain.Role) error
	RemoveMember(ctx context.Context, familyID, userID int64) error

	CreateInvite(ctx context.Context, inv domain.Invite) (domain.Invite, error)
	UseInvite(ctx context.Context, code string) (domain.Invite, error)
	ListActiveInvites(ctx context.Context, familyID int64) ([]domain.Invite, error)
	RevokeInvite(ctx context.Context, familyID, inviteID int64) error
}

// AuthStore - одноразовые ссылки входа и сессии панели.
type AuthStore interface {
	Transactor
	membershipReader
	CreateLoginToken(ctx context.Context, tokenHash []byte, userID int64, expiresAt time.Time) error
	UseLoginToken(ctx context.Context, tokenHash []byte) (int64, error)
	CreateSession(ctx context.Context, idHash []byte, userID int64, expiresAt time.Time) error
	SessionUser(ctx context.Context, idHash []byte) (int64, error)
	DeleteSession(ctx context.Context, idHash []byte) error
}

// PanelStore - поиск владельца для режима панели без входа.
type PanelStore interface {
	SoleFamilyID(ctx context.Context) (int64, error)
	FamilyOwner(ctx context.Context, familyID int64) (domain.Member, error)
}
