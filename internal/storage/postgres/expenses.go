package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"expense-bot/internal/domain"
)

const expenseColumns = `id, family_id, user_id, amount_minor, category, spent_at, title, comment, version, created_at, updated_at`

func scanExpense(row pgx.Row) (domain.Expense, error) {
	var (
		e       domain.Expense
		spentAt time.Time
	)
	err := row.Scan(&e.ID, &e.FamilyID, &e.UserID, &e.Amount, &e.Category, &spentAt,
		&e.Title, &e.Comment, &e.Version, &e.CreatedAt, &e.UpdatedAt)
	e.SpentAt = domain.DateOf(spentAt, time.UTC)
	return e, err
}

func collectExpenses(rows pgx.Rows) ([]domain.Expense, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Expense, error) { return scanExpense(row) })
}

// scopeWhere возвращает условие области видимости и добавляет его аргументы.
// Обязателен в каждом запросе к expenses.
func scopeWhere(scope domain.ExpenseScope, args *[]any) (string, error) {
	if !scope.Valid() {
		return "", fmt.Errorf("invalid expense scope: %w", domain.ErrForbidden)
	}
	*args = append(*args, scope.FamilyID())
	cond := "family_id = $" + strconv.Itoa(len(*args))
	if uid, ok := scope.UserID(); ok {
		*args = append(*args, uid)
		cond += " AND user_id = $" + strconv.Itoa(len(*args))
	}
	return cond, nil
}

// CreateExpense сохраняет трату. FamilyID и UserID берутся из e и должны прийти из Actor.
func (s *Store) CreateExpense(ctx context.Context, e domain.Expense) (domain.Expense, error) {
	res, err := scanExpense(s.conn(ctx).QueryRow(ctx, `
		INSERT INTO expenses (family_id, user_id, amount_minor, category, spent_at, title, comment)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+expenseColumns,
		e.FamilyID, e.UserID, e.Amount, e.Category, e.SpentAt.Time(), e.Title, e.Comment))
	return res, mapErr("create expense", err)
}

func (s *Store) GetExpense(ctx context.Context, scope domain.ExpenseScope, id int64) (domain.Expense, error) {
	args := []any{id}
	where, err := scopeWhere(scope, &args)
	if err != nil {
		return domain.Expense{}, err
	}
	e, err := scanExpense(s.conn(ctx).QueryRow(ctx,
		`SELECT `+expenseColumns+` FROM expenses WHERE id = $1 AND `+where, args...))
	return e, mapErr("get expense", err)
}

// LastExpense - последняя добавленная трата (по created_at, а не по spent_at).
func (s *Store) LastExpense(ctx context.Context, scope domain.ExpenseScope) (domain.Expense, error) {
	var args []any
	where, err := scopeWhere(scope, &args)
	if err != nil {
		return domain.Expense{}, err
	}
	e, err := scanExpense(s.conn(ctx).QueryRow(ctx,
		`SELECT `+expenseColumns+` FROM expenses WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT 1`, args...))
	return e, mapErr("last expense", err)
}

// filterWhere строит WHERE для ListExpenses и SumExpenses.
func filterWhere(f domain.ExpenseFilter, args *[]any) (string, error) {
	where, err := scopeWhere(f.Scope, args)
	if err != nil {
		return "", err
	}
	conds := []string{where}
	arg := func(v any) string {
		*args = append(*args, v)
		return "$" + strconv.Itoa(len(*args))
	}
	if f.MemberID != 0 {
		conds = append(conds, "user_id = "+arg(f.MemberID))
	}
	if !f.From.IsZero() {
		conds = append(conds, "spent_at >= "+arg(f.From.Time()))
	}
	if !f.To.IsZero() {
		conds = append(conds, "spent_at <= "+arg(f.To.Time()))
	}
	if len(f.Categories) > 0 {
		cats := make([]string, len(f.Categories))
		for i, c := range f.Categories {
			cats[i] = string(c)
		}
		conds = append(conds, "category = ANY("+arg(cats)+")")
	}
	if len(f.Terms) > 0 {
		patterns := make([]string, 0, len(f.Terms))
		for _, t := range f.Terms {
			if t = strings.TrimSpace(t); t != "" {
				patterns = append(patterns, "%"+escapeLike(t)+"%")
			}
		}
		if len(patterns) > 0 {
			p := arg(patterns)
			conds = append(conds, "(title ILIKE ANY("+p+") OR comment ILIKE ANY("+p+"))")
		}
	}
	return strings.Join(conds, " AND "), nil
}

// escapeLike экранирует спецсимволы LIKE (экранирующий символ по умолчанию - обратный слеш).
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListExpenses возвращает траты по фильтру: новые даты первыми, внутри даты - последние добавленные.
func (s *Store) ListExpenses(ctx context.Context, f domain.ExpenseFilter) ([]domain.Expense, error) {
	var args []any
	where, err := filterWhere(f, &args)
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = domain.DefaultLimit
	}
	limit = min(limit, domain.MaxLimit)
	args = append(args, limit, max(f.Offset, 0))
	n := len(args)

	rows, err := s.conn(ctx).Query(ctx, `SELECT `+expenseColumns+` FROM expenses WHERE `+where+
		` ORDER BY spent_at DESC, created_at DESC, id DESC LIMIT $`+strconv.Itoa(n-1)+` OFFSET $`+strconv.Itoa(n), args...)
	if err != nil {
		return nil, mapErr("list expenses", err)
	}
	res, err := collectExpenses(rows)
	return res, mapErr("list expenses", err)
}

// SumExpenses возвращает итог и число трат по фильтру без учёта Limit и Offset.
func (s *Store) SumExpenses(ctx context.Context, f domain.ExpenseFilter) (domain.Money, int, error) {
	var args []any
	where, err := filterWhere(f, &args)
	if err != nil {
		return 0, 0, err
	}
	var (
		total domain.Money
		count int
	)
	err = s.conn(ctx).QueryRow(ctx,
		`SELECT COALESCE(sum(amount_minor), 0), count(*) FROM expenses WHERE `+where, args...).Scan(&total, &count)
	return total, count, mapErr("sum expenses", err)
}

// UpdateExpense меняет сумму, категорию, дату, название и комментарий при совпадении e.Version
// (оптимистичная блокировка). ErrConflict - запись изменили параллельно, ErrNotFound - её нет в scope.
func (s *Store) UpdateExpense(ctx context.Context, scope domain.ExpenseScope, e domain.Expense) (domain.Expense, error) {
	args := []any{e.ID, e.Version, e.Amount, e.Category, e.SpentAt.Time(), e.Title, e.Comment}
	where, err := scopeWhere(scope, &args)
	if err != nil {
		return domain.Expense{}, err
	}
	res, err := scanExpense(s.conn(ctx).QueryRow(ctx, `
		UPDATE expenses
		SET amount_minor = $3, category = $4, spent_at = $5, title = $6, comment = $7,
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 AND `+where+`
		RETURNING `+expenseColumns, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		// Различаем «нет такой записи» и «версия устарела».
		if _, getErr := s.GetExpense(ctx, scope, e.ID); getErr != nil {
			return domain.Expense{}, fmt.Errorf("update expense: %w", getErr)
		}
		return domain.Expense{}, fmt.Errorf("update expense: %w", domain.ErrConflict)
	}
	return res, mapErr("update expense", err)
}

// DeleteExpense удаляет трату в пределах scope.
func (s *Store) DeleteExpense(ctx context.Context, scope domain.ExpenseScope, id int64) error {
	args := []any{id}
	where, err := scopeWhere(scope, &args)
	if err != nil {
		return err
	}
	return s.execOne(ctx, "delete expense", `DELETE FROM expenses WHERE id = $1 AND `+where, args...)
}

// SummarizeExpenses - итоги по категориям, участникам и дням для того же фильтра,
// что и ListExpenses (без Limit/Offset). Агрегация в БД, а не по странице на клиенте.
func (s *Store) SummarizeExpenses(ctx context.Context, f domain.ExpenseFilter) (domain.ExpenseSummary, error) {
	var args []any
	where, err := filterWhere(f, &args)
	if err != nil {
		return domain.ExpenseSummary{}, err
	}
	var sum domain.ExpenseSummary
	if sum.Total, sum.Count, err = s.SumExpenses(ctx, f); err != nil {
		return domain.ExpenseSummary{}, err
	}
	rows, err := s.conn(ctx).Query(ctx, `SELECT category, sum(amount_minor), count(*) FROM expenses WHERE `+where+
		` GROUP BY category ORDER BY sum(amount_minor) DESC, category`, args...)
	if err != nil {
		return domain.ExpenseSummary{}, mapErr("summary by category", err)
	}
	if sum.ByCategory, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.CategoryTotal, error) {
		var t domain.CategoryTotal
		return t, r.Scan(&t.Category, &t.Total, &t.Count)
	}); err != nil {
		return domain.ExpenseSummary{}, mapErr("summary by category", err)
	}
	rows, err = s.conn(ctx).Query(ctx, `SELECT user_id, sum(amount_minor), count(*) FROM expenses WHERE `+where+
		` GROUP BY user_id ORDER BY sum(amount_minor) DESC, user_id`, args...)
	if err != nil {
		return domain.ExpenseSummary{}, mapErr("summary by member", err)
	}
	if sum.ByMember, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.MemberTotal, error) {
		var t domain.MemberTotal
		return t, r.Scan(&t.UserID, &t.Total, &t.Count)
	}); err != nil {
		return domain.ExpenseSummary{}, mapErr("summary by member", err)
	}
	rows, err = s.conn(ctx).Query(ctx, `SELECT spent_at, sum(amount_minor) FROM expenses WHERE `+where+
		` GROUP BY spent_at ORDER BY spent_at LIMIT 400`, args...)
	if err != nil {
		return domain.ExpenseSummary{}, mapErr("summary by day", err)
	}
	if sum.ByDay, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.DayTotal, error) {
		var (
			t domain.DayTotal
			d time.Time
		)
		err := r.Scan(&d, &t.Total)
		t.Date = domain.DateOf(d, time.UTC)
		return t, err
	}); err != nil {
		return domain.ExpenseSummary{}, mapErr("summary by day", err)
	}
	return sum, nil
}
