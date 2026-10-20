package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"expense-bot/internal/domain"
)

// Коды ошибок PostgreSQL, см. https://www.postgresql.org/docs/16/errcodes-appendix.html
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
)

// Имена ограничений из миграций, по которым различаются ошибки уникальности.
const (
	constraintMembersPK   = "family_members_pkey"
	constraintFamilyOwner = "uniq_family_owner"
	constraintInviteCode  = "invites_code_key"
	constraintExpenseCat  = "expenses_category_fkey"
	constraintLearnedCat  = "learned_terms_category_fkey"
)

// mapErr переводит ошибки драйвера в доменные, сохраняя исходную в цепочке.
func mapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", op, domain.ErrNotFound)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			switch pgErr.ConstraintName {
			case constraintMembersPK:
				return fmt.Errorf("%s: %w", op, domain.ErrInFamily)
			case constraintInviteCode:
				return fmt.Errorf("%s: %w", op, domain.ErrDuplicateInviteCode)
			case constraintFamilyOwner:
				return fmt.Errorf("%s: family already has an owner: %w", op, domain.ErrConflict)
			}
			return fmt.Errorf("%s: %w: %s", op, domain.ErrConflict, pgErr.ConstraintName)
		case pgForeignKeyViolation:
			if pgErr.ConstraintName == constraintExpenseCat || pgErr.ConstraintName == constraintLearnedCat {
				return fmt.Errorf("%s: %w: unknown category", op, domain.ErrValidation)
			}
			return fmt.Errorf("%s: %w: %s", op, domain.ErrNotFound, pgErr.ConstraintName)
		case pgCheckViolation:
			return fmt.Errorf("%s: %w: %s", op, domain.ErrValidation, pgErr.ConstraintName)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}
