// Клиент к LLM. Интерфейс позволяет подменить провайдера через конфиг и мокать в тестах.
package llm

import (
	"context"
	"time"
)

type ParsedExpense struct {
	IsExpense   bool
	Amount      int64 // копейки
	Category    string
	Description string
	SpentAt     time.Time
}

type Parser interface {
	// ParseExpense разбирает текст; now и loc нужны для "вчера", "в пятницу".
	ParseExpense(ctx context.Context, text string, now time.Time, loc *time.Location) (*ParsedExpense, error)
}
