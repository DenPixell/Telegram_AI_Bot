package domain

import "errors"

var (
	ErrNotFound       = errors.New("not found")
	ErrNotInFamily    = errors.New("user is not in a family")
	ErrForbidden      = errors.New("forbidden")
	ErrNotAnExpense   = errors.New("message is not an expense")
	ErrInviteNotValid = errors.New("invite code is not valid")
)
