// Дешёвый фильтр ДО обращения к LLM: отсекает сообщения, которые точно не траты,
// чтобы не тратить токены ("сделай домашку", "какая ты модель").
package filter

import (
	"regexp"
	"strings"
)

var amountRe = regexp.MustCompile(`\d+([.,]\d{1,2})?`)

// LooksLikeExpense - эвристика: короткое сообщение с числом и без признаков вопроса/задачи.
func LooksLikeExpense(text string) bool {
	t := strings.TrimSpace(strings.ToLower(text))
	if t == "" || len([]rune(t)) > 120 {
		return false
	}
	if !amountRe.MatchString(t) {
		return false
	}
	if strings.HasSuffix(t, "?") {
		return false
	}
	return true
}
