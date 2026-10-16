package domain

import "time"

type Expense struct {
	ID          int64
	FamilyID    int64
	UserID      int64
	Amount      int64 // в копейках, без float
	Currency    string
	CategoryID  int64
	Description string
	SpentAt     time.Time // хранится в UTC
	RawText     string    // исходное сообщение, для разбора ошибок
	CreatedAt   time.Time
}
