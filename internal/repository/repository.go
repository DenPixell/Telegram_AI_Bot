// Интерфейсы хранилища. Сервисы зависят только от них.
package repository

import (
	"context"
	"time"

	"Telegram_AI_Bot/internal/domain"
)

type Users interface {
	GetByTelegramID(ctx context.Context, tgID int64) (*domain.User, error)
	Create(ctx context.Context, u *domain.User) error
	Update(ctx context.Context, u *domain.User) error
	ListByFamily(ctx context.Context, familyID int64) ([]domain.User, error)
}

type Families interface {
	Create(ctx context.Context, f *domain.Family) error
	GetByID(ctx context.Context, id int64) (*domain.Family, error)
	GetByInviteCode(ctx context.Context, code string) (*domain.Family, error)
}

type Expenses interface {
	Create(ctx context.Context, e *domain.Expense) error
	Delete(ctx context.Context, id, familyID int64) error
	List(ctx context.Context, f ExpenseFilter) ([]domain.Expense, error)
}

type Categories interface {
	ListForFamily(ctx context.Context, familyID int64) ([]domain.Category, error)
	GetOrCreate(ctx context.Context, familyID int64, name string) (*domain.Category, error)
}

type ExpenseFilter struct {
	FamilyID   int64
	UserID     *int64
	CategoryID *int64
	From, To   time.Time
	Limit      int
	Offset     int
}
