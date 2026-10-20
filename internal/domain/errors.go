package domain

import "errors"

var (
	ErrNotFound    = errors.New("not found")
	ErrForbidden   = errors.New("forbidden")
	ErrConflict    = errors.New("conflict: record was modified") // несовпадение version
	ErrValidation  = errors.New("validation failed")
	ErrNotInFamily = errors.New("user is not in a family")
	ErrInFamily    = errors.New("user is already in a family")
	ErrFamilyFull  = errors.New("family member limit reached")
	// ErrInviteInvalid - одна ошибка для неверного, истёкшего и отозванного кода,
	// чтобы не давать информации перебору.
	ErrInviteInvalid = errors.New("invite code not found or no longer valid")
	// ErrDuplicateInviteCode - коллизия кода приглашения: генерируем новый код и повторяем.
	ErrDuplicateInviteCode = errors.New("duplicate invite code")
	ErrTooManyAttempts     = errors.New("too many attempts")
	ErrOwnerMustStay       = errors.New("owner cannot leave without transferring ownership")
)
