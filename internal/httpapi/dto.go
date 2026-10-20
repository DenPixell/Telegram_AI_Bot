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
	SpentAt   domain.Date  `json:"spent_at"` // дата траты, как в модели данных
	Title     string       `json:"title"`
	Comment   string       `json:"comment"`
	Version   int          `json:"version"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func toExpenseDTO(e domain.Expense) expenseDTO {
	return expenseDTO{
		ID: e.ID, UserID: e.UserID, Amount: e.Amount, Category: string(e.Category), SpentAt: e.SpentAt,
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

type summaryDTO struct {
	Total      domain.Money       `json:"total"`
	Count      int                `json:"count"`
	ByCategory []categoryTotalDTO `json:"by_category"`
	ByMember   []memberTotalDTO   `json:"by_member"`
	ByDay      []dayTotalDTO      `json:"by_day"`
}

type categoryTotalDTO struct {
	Category string       `json:"category"`
	Total    domain.Money `json:"total"`
	Count    int          `json:"count"`
}

type memberTotalDTO struct {
	UserID int64        `json:"user_id"`
	Total  domain.Money `json:"total"`
	Count  int          `json:"count"`
}

type dayTotalDTO struct {
	Date  domain.Date  `json:"date"`
	Total domain.Money `json:"total"`
}

func toSummaryDTO(s domain.ExpenseSummary) summaryDTO {
	return summaryDTO{
		Total: s.Total,
		Count: s.Count,
		ByCategory: mapSlice(s.ByCategory, func(c domain.CategoryTotal) categoryTotalDTO {
			return categoryTotalDTO{Category: string(c.Category), Total: c.Total, Count: c.Count}
		}),
		ByMember: mapSlice(s.ByMember, func(m domain.MemberTotal) memberTotalDTO {
			return memberTotalDTO{UserID: m.UserID, Total: m.Total, Count: m.Count}
		}),
		ByDay: mapSlice(s.ByDay, func(d domain.DayTotal) dayTotalDTO {
			return dayTotalDTO{Date: d.Date, Total: d.Total}
		}),
	}
}

type categoryDTO struct {
	Name  domain.Category `json:"name"`
	Title string          `json:"title"`
	Emoji string          `json:"emoji"`
}

func categoryDTOFrom(c domain.CategoryInfo) categoryDTO {
	return categoryDTO{Name: c.Name, Title: c.Title, Emoji: c.Emoji}
}
