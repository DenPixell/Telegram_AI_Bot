package domain

import "time"

type Role string

const (
	RoleHead   Role = "head"   // глава семьи, доступ к панели
	RoleMember Role = "member" // обычный участник
)

type User struct {
	ID         int64
	TelegramID int64
	Name       string
	FamilyID   *int64
	Role       Role
	Title      string // "жена", "муж", "сын" - выдаётся в панели
	Timezone   string // IANA, например "Europe/Moscow"
	CreatedAt  time.Time
}
