package httpapi

import (
	"time"

	"expense-bot/internal/domain"
	"expense-bot/internal/service"
)

// Деньги - строка "350.00", даты - "YYYY-MM-DD", моменты времени - RFC 3339 в UTC.

type expenseDTO struct {
	ID        int64        `json:"id"`
	UserID    int64        `json:"user_id"`
	Amount    domain.Money `json:"amount"`
	Category  string       `json:"category"`
	Date      domain.Date  `json:"date"`
	Title     string       `json:"title"`
	Comment   string       `json:"comment"`
	Version   int          `json:"version"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func toExpenseDTO(e domain.Expense) expenseDTO {
	return expenseDTO{
		ID: e.ID, UserID: e.UserID, Amount: e.Amount, Category: string(e.Category), Date: e.SpentAt,
		Title: e.Title, Comment: e.Comment, Version: e.Version, CreatedAt: e.CreatedAt.UTC(), UpdatedAt: e.UpdatedAt.UTC(),
	}
}

type expenseListDTO struct {
	Items  []expenseDTO `json:"items"`
	Total  domain.Money `json:"total"` // сумма по фильтру без учёта limit/offset
	Count  int          `json:"count"` // число записей по фильтру без учёта limit/offset
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}

type familyDTO struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Timezone  string    `json:"timezone"`
	CreatedAt time.Time `json:"created_at"`
}

func toFamilyDTO(f domain.Family) familyDTO {
	return familyDTO{ID: f.ID, Name: f.Name, Timezone: f.DefaultTZ, CreatedAt: f.CreatedAt.UTC()}
}

type memberDTO struct {
	UserID      int64     `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Username    string    `json:"username"`
	Role        string    `json:"role"`
	Relation    string    `json:"relation"`
	JoinedAt    time.Time `json:"joined_at"`
}

func toMemberDTO(m domain.MemberProfile) memberDTO {
	return memberDTO{
		UserID: m.UserID, DisplayName: m.DisplayName, Username: m.Username,
		Role: string(m.Role), Relation: m.Relation, JoinedAt: m.JoinedAt.UTC(),
	}
}

type inviteDTO struct {
	ID        int64     `json:"id"`
	Code      string    `json:"code"`           // вид для человека: K7M2Q-9XH4T
	Link      string    `json:"link,omitempty"` // пусто, пока неизвестен username бота
	ExpiresAt time.Time `json:"expires_at"`
	MaxUses   int       `json:"max_uses"`
	UsedCount int       `json:"used_count"`
	CreatedAt time.Time `json:"created_at"`
}

func toInviteDTO(i domain.Invite, botUsername string) inviteDTO {
	link := ""
	if botUsername != "" {
		link = service.InviteLink(botUsername, i.Code)
	}
	return inviteDTO{
		ID: i.ID, Code: service.FormatInviteCode(i.Code), Link: link, ExpiresAt: i.ExpiresAt.UTC(),
		MaxUses: i.MaxUses, UsedCount: i.UsedCount, CreatedAt: i.CreatedAt.UTC(),
	}
}

func mapSlice[T, R any](in []T, f func(T) R) []R {
	out := make([]R, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}
