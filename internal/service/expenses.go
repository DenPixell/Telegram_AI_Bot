package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"expense-bot/internal/domain"
	"expense-bot/internal/storage/postgres"
	"expense-bot/internal/timezone"
)

const (
	MaxTitleLen = 64
	// Окно допустимых дат траты относительно «сегодня» пользователя.
	maxPastYears  = 5
	maxFutureDays = 1
)

// Expenses - траты. Бот работает в UserScope (только свои), панель владельца - в FamilyScope.
// Доработка для бота (разрешение «последней», синонимы) - этап 7.
type Expenses struct {
	store *postgres.Store
	appTZ string
	now   func() time.Time
}

func NewExpenses(store *postgres.Store, appTZ string) *Expenses {
	return &Expenses{store: store, appTZ: appTZ, now: time.Now}
}

// PanelScope - все траты семьи; только для владельца.
func PanelScope(a domain.Actor) (domain.ExpenseScope, error) {
	if err := requireOwner(a); err != nil {
		return domain.ExpenseScope{}, err
	}
	return domain.FamilyScope(a.FamilyID), nil
}

func (s *Expenses) List(ctx context.Context, f domain.ExpenseFilter) ([]domain.Expense, domain.Money, int, error) {
	if !f.From.IsZero() && !f.To.IsZero() && f.To.Before(f.From) {
		return nil, 0, 0, fmt.Errorf("%w: начало периода позже конца", domain.ErrValidation)
	}
	items, err := s.store.ListExpenses(ctx, f)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("list expenses: %w", err)
	}
	total, count, err := s.store.SumExpenses(ctx, f)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("list expenses: %w", err)
	}
	return items, total, count, nil
}

func (s *Expenses) Get(ctx context.Context, scope domain.ExpenseScope, id int64) (domain.Expense, error) {
	e, err := s.store.GetExpense(ctx, scope, id)
	if err != nil {
		return domain.Expense{}, fmt.Errorf("get expense: %w", err)
	}
	return e, nil
}

// Create создаёт трату в семье actor. e.UserID = 0 - трата самого actor; трату другого
// участника может создать только владелец, и этот участник должен быть в его семье.
// Нулевая дата - сегодня в поясе пользователя.
func (s *Expenses) Create(ctx context.Context, a domain.Actor, e domain.Expense) (domain.Expense, error) {
	e.FamilyID = a.FamilyID
	if e.UserID == 0 {
		e.UserID = a.UserID
	}
	if e.UserID != a.UserID {
		if err := requireOwner(a); err != nil {
			return domain.Expense{}, fmt.Errorf("create expense: %w", err)
		}
		m, err := s.store.GetMembership(ctx, e.UserID)
		if err != nil || m.FamilyID != a.FamilyID {
			return domain.Expense{}, fmt.Errorf("create expense: %w: этот человек не состоит в семье", domain.ErrValidation)
		}
	}
	today, err := s.Today(ctx, e.UserID)
	if err != nil {
		return domain.Expense{}, err
	}
	if e.SpentAt.IsZero() {
		e.SpentAt = today
	}
	if e, err = validateExpense(e, today); err != nil {
		return domain.Expense{}, err
	}
	res, err := s.store.CreateExpense(ctx, e)
	if err != nil {
		return domain.Expense{}, fmt.Errorf("create expense: %w", err)
	}
	return res, nil
}

// Update заменяет поля траты при совпадении e.Version. ErrConflict - запись изменили параллельно.
func (s *Expenses) Update(ctx context.Context, scope domain.ExpenseScope, e domain.Expense) (domain.Expense, error) {
	cur, err := s.store.GetExpense(ctx, scope, e.ID)
	if err != nil {
		return domain.Expense{}, fmt.Errorf("update expense: %w", err)
	}
	today, err := s.Today(ctx, cur.UserID)
	if err != nil {
		return domain.Expense{}, err
	}
	if e, err = validateExpense(e, today); err != nil {
		return domain.Expense{}, err
	}
	res, err := s.store.UpdateExpense(ctx, scope, e)
	if err != nil {
		return domain.Expense{}, fmt.Errorf("update expense: %w", err)
	}
	return res, nil
}

func (s *Expenses) Delete(ctx context.Context, scope domain.ExpenseScope, id int64) error {
	if err := s.store.DeleteExpense(ctx, scope, id); err != nil {
		return fmt.Errorf("delete expense: %w", err)
	}
	return nil
}

// Last - последняя добавленная трата в scope (по created_at).
func (s *Expenses) Last(ctx context.Context, scope domain.ExpenseScope) (domain.Expense, error) {
	e, err := s.store.LastExpense(ctx, scope)
	if err != nil {
		return domain.Expense{}, fmt.Errorf("last expense: %w", err)
	}
	return e, nil
}

// Today - текущая дата в поясе пользователя (users.timezone -> пояс семьи -> APP_TZ).
func (s *Expenses) Today(ctx context.Context, userID int64) (domain.Date, error) {
	u, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return domain.Date{}, fmt.Errorf("expense owner: %w", err)
	}
	familyTZ := ""
	if m, err := s.store.GetMembership(ctx, userID); err == nil {
		if f, err := s.store.GetFamily(ctx, m.FamilyID); err == nil {
			familyTZ = f.DefaultTZ
		}
	}
	return timezone.Today(s.now(), timezone.Resolve(u.Timezone, familyTZ, s.appTZ)), nil
}

// validateExpense проверяет и нормализует поля, которые задаёт пользователь или модель.
func validateExpense(e domain.Expense, today domain.Date) (domain.Expense, error) {
	if !e.Amount.Valid() {
		return e, fmt.Errorf("%w: сумма должна быть от 0.01 до %s", domain.ErrValidation, domain.MaxMoney)
	}
	if !e.Category.Valid() {
		return e, fmt.Errorf("%w: неизвестная категория %q", domain.ErrValidation, e.Category)
	}
	if e.SpentAt.IsZero() {
		return e, fmt.Errorf("%w: укажите дату", domain.ErrValidation)
	}
	earliest := domain.Date{Year: today.Year - maxPastYears, Month: today.Month, Day: 1}
	if e.SpentAt.Before(earliest) || today.AddDays(maxFutureDays).Before(e.SpentAt) {
		return e, fmt.Errorf("%w: дата %s вне допустимого диапазона (не раньше чем 5 лет назад и не позже завтра)", domain.ErrValidation, e.SpentAt)
	}
	e.Title = strings.TrimSpace(stripNUL(e.Title))
	if utf8.RuneCountInString(e.Title) > MaxTitleLen {
		return e, fmt.Errorf("%w: название длиннее %d символов", domain.ErrValidation, MaxTitleLen)
	}
	e.Comment = truncateRunes(strings.TrimSpace(stripNUL(e.Comment)), domain.MaxCommentLen)
	return e, nil
}

// stripNUL убирает нулевые символы: PostgreSQL не хранит их в TEXT, а JSON (\u0000) их пропускает.
func stripNUL(s string) string { return strings.ReplaceAll(s, "\x00", "") }

// truncateRunes обрезает строку до n символов (не байт).
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Summary - итоги для сводки панели по тому же фильтру, что и List.
func (s *Expenses) Summary(ctx context.Context, f domain.ExpenseFilter) (domain.ExpenseSummary, error) {
	if !f.From.IsZero() && !f.To.IsZero() && f.To.Before(f.From) {
		return domain.ExpenseSummary{}, fmt.Errorf("%w: начало периода позже конца", domain.ErrValidation)
	}
	sum, err := s.store.SummarizeExpenses(ctx, f)
	if err != nil {
		return domain.ExpenseSummary{}, fmt.Errorf("expense summary: %w", err)
	}
	return sum, nil
}
