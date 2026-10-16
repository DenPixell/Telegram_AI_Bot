package domain

import "time"

type Family struct {
	ID         int64
	Name       string
	InviteCode string // уникальный код для вступления
	HeadUserID int64
	CreatedAt  time.Time
}
