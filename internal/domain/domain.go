// Package domain содержит сущности и доменные ошибки. Без зависимостей от других пакетов проекта.
package domain

import (
	"encoding/json"
	"time"
)

// Role определяет права в семье. Панель доступна только владельцу.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

func (r Role) Valid() bool { return r == RoleOwner || r == RoleMember }

// UserState - шаг конечного автомата онбординга (users.state).
type UserState string

const (
	StateNew                UserState = "new"
	StateAwaitingChoice     UserState = "awaiting_choice"
	StateAwaitingFamilyName UserState = "awaiting_family_name"
	StateAwaitingTZ         UserState = "awaiting_tz"
	StateActive             UserState = "active"
)

// Actor - кто выполняет действие. Берётся только из Telegram-апдейта или сессии панели,
// никогда из ответа модели или тела запроса.
type Actor struct {
	UserID   int64 // Telegram ID
	FamilyID int64
	Role     Role
}

func (a Actor) IsOwner() bool { return a.Role == RoleOwner }

type Family struct {
	ID        int64
	Name      string
	DefaultTZ string // IANA
	CreatedAt time.Time
}

type User struct {
	TelegramID  int64
	DisplayName string
	Username    string
	Timezone    string // IANA, "" = взять у семьи
	State       UserState
	StateData   json.RawMessage
	IsActive    bool // false, если пользователь заблокировал бота
	CreatedAt   time.Time
}

// Member - участие пользователя в семье (одна семья на пользователя).
type Member struct {
	UserID   int64
	FamilyID int64
	Role     Role
	Relation string // подпись для отображения: «жена», «сын»; на права не влияет
	JoinedAt time.Time
}

type Invite struct {
	ID        int64
	FamilyID  int64
	Code      string // нормализованный вид, 10 символов
	CreatedBy int64
	ExpiresAt time.Time
	MaxUses   int
	UsedCount int
	RevokedAt *time.Time
	CreatedAt time.Time
}

// Usable сообщает, можно ли ещё вступить по приглашению в момент now.
func (i Invite) Usable(now time.Time) bool {
	return i.RevokedAt == nil && now.Before(i.ExpiresAt) && i.UsedCount < i.MaxUses
}

type Expense struct {
	ID        int64
	FamilyID  int64
	UserID    int64
	Amount    Money
	Category  Category
	SpentAt   Date   // локальная дата пользователя на момент траты
	Title     string // канон из словаря: «ресторан»
	Comment   string // как написал пользователь, до 200 символов
	Version   int    // оптимистичная блокировка
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MaxCommentLen совпадает с CHECK в таблице expenses.
const MaxCommentLen = 200

// LearnedTerm - слово вне словаря, категория которого найдена онлайн-тезаурусом или LLM.
// Category "" - слово проверено, но не распознано (отрицательный кэш).
type LearnedTerm struct {
	Stem      string
	Word      string
	Title     string
	Category  Category
	Source    string // "wiktionary" | "llm"
	UpdatedAt time.Time
}

// BroadcastStatus - этап рассылки.
type BroadcastStatus string

const (
	BroadcastDraft     BroadcastStatus = "draft"     // ждёт подтверждения
	BroadcastSending   BroadcastStatus = "sending"   // идёт отправка
	BroadcastDone      BroadcastStatus = "done"      // завершена
	BroadcastCancelled BroadcastStatus = "cancelled" // отменена до отправки
)

// MaxBroadcastLen совпадает с CHECK в таблице broadcasts (лимит Telegram - 4096).
const MaxBroadcastLen = 4000

// Broadcast - сообщение всем пользователям бота.
type Broadcast struct {
	ID         int64
	Text       string
	CreatedBy  int64 // Telegram ID администратора; 0 - через HTTP API
	Status     BroadcastStatus
	Total      int
	Sent       int
	Failed     int
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}
