package agent

import (
	"fmt"
	"strconv"
	"strings"

	"expense-bot/internal/domain"
	"expense-bot/internal/pipeline"
	"expense-bot/internal/timezone"
)

// Все ответы пользователю - шаблоны из фактического результата. Обычный текст без разметки:
// данные пользователя не вставляются в Markdown/HTML.

const (
	msgOffTopic      = "Я веду только учёт расходов 🙂\nПримеры: «кофе 350», «такси 1200 вчера», «сколько потратил на еду в этом месяце»."
	msgTooLong       = "Сообщение слишком длинное. Напишите трату коротко, например: «продукты 2400»."
	msgEmpty         = "Напишите трату текстом, например: «кофе 350»."
	msgNotUnderstood = "Не понял. Попробуйте иначе, например: «кофе 350» или «удали последнюю трату»."
	msgUnavailable   = "ИИ сейчас недоступен, попробуйте чуть позже. Простые траты вида «кофе 350» записываются и без него."
	msgBusy          = "Сейчас много запросов, повторите через минуту."
	msgQuota         = "Лимит запросов к ИИ на сегодня исчерпан. Простые траты вида «кофе 350» по-прежнему записываются."
	msgNoAmount      = "Сколько потратили? Напишите сумму, например: «кофе 350»."
	msgAmbiguous     = "Не понял, о какой трате речь. Уточните, например: «удали вчерашнее такси»."
	msgUnknownDate   = "Не понял дату. Напишите, например: «вчера», «в пятницу» или «12.10»."
	msgCancelled     = "Хорошо, ничего не меняю."
	msgNothingFound  = "Не нашёл такой траты."
	msgChooseNumber  = "Напишите номер из списка или «отмена»."
	msgError         = "Что-то пошло не так, попробуйте ещё раз."
)

func rejectMessage(r pipeline.RejectReason) string {
	switch r {
	case pipeline.ReasonTooLong:
		return msgTooLong
	case pipeline.ReasonEmpty:
		return msgEmpty
	}
	return msgOffTopic
}

func clarifyMessage(code string) string {
	switch code {
	case clarifyNoAmount:
		return msgNoAmount
	case clarifyUnknownDate:
		return msgUnknownDate
	}
	return msgAmbiguous
}

// money: 1200 -> «1 200 ₽», 350.5 -> «350,50 ₽».
func money(m domain.Money) string {
	whole := strconv.FormatInt(int64(m)/100, 10)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	if k := int64(m) % 100; k != 0 {
		fmt.Fprintf(&b, ",%02d", k)
	}
	return b.String() + " ₽"
}

// day: «сегодня», «вчера», «15.10», «15.10.2025».
func day(d, today domain.Date) string {
	switch d {
	case today:
		return "сегодня"
	case today.AddDays(-1):
		return "вчера"
	}
	if d.Year == today.Year {
		return fmt.Sprintf("%02d.%02d", d.Day, d.Month)
	}
	return fmt.Sprintf("%02d.%02d.%d", d.Day, d.Month, d.Year)
}

func title(e domain.Expense) string {
	if e.Title != "" {
		return e.Title
	}
	if e.Comment != "" {
		return e.Comment
	}
	return string(e.Category)
}

// line - одна трата: «кофе — 350 ₽ · еда · вчера».
func line(e domain.Expense, today domain.Date) string {
	return fmt.Sprintf("%s — %s · %s · %s", title(e), money(e.Amount), e.Category, day(e.SpentAt, today))
}

func renderAdded(e domain.Expense, today domain.Date) string {
	return "✅ Записал: " + line(e, today)
}

func renderDeleted(e domain.Expense, today domain.Date) string {
	return "🗑 Удалил: " + line(e, today)
}

func renderUpdated(before, after domain.Expense, today domain.Date) string {
	var changes []string
	if before.Amount != after.Amount {
		changes = append(changes, fmt.Sprintf("сумма %s → %s", money(before.Amount), money(after.Amount)))
	}
	if before.Category != after.Category {
		changes = append(changes, fmt.Sprintf("категория %s → %s", before.Category, after.Category))
	}
	if before.SpentAt != after.SpentAt {
		changes = append(changes, fmt.Sprintf("дата %s → %s", day(before.SpentAt, today), day(after.SpentAt, today)))
	}
	if before.Title != after.Title {
		changes = append(changes, fmt.Sprintf("название «%s» → «%s»", before.Title, after.Title))
	}
	if len(changes) == 0 {
		return "Ничего не изменилось: " + line(after, today)
	}
	return "✏️ Исправил: " + line(after, today) + "\n(" + strings.Join(changes, ", ") + ")"
}

func renderNeedAmount(t string) string {
	if t == "" {
		return msgNoAmount
	}
	return fmt.Sprintf("Сколько потратили на «%s»? Напишите сумму, например: 350.", t)
}

func renderConfirmLarge(e domain.Expense, today domain.Date) string {
	return fmt.Sprintf("Сумма большая: %s. Записать «%s»? Ответьте «да» или «нет».", money(e.Amount), title(e))
}

var periodNames = map[timezone.Period]string{
	timezone.PeriodToday: "сегодня", timezone.PeriodYesterday: "вчера", timezone.PeriodWeek: "эту неделю",
	timezone.PeriodMonth: "этот месяц", timezone.PeriodLastMonth: "прошлый месяц", timezone.PeriodAll: "всё время",
}

// maxListed - сколько трат показывать в ответе; остальное - итогом.
const maxListed = 10

func renderList(p timezone.Period, from, to domain.Date, cat domain.Category, query string, items []domain.Expense, total domain.Money, count int, today domain.Date) string {
	when := periodNames[p]
	if p == timezone.PeriodCustom || when == "" {
		switch {
		case !from.IsZero() && !to.IsZero():
			when = "период " + day(from, today) + " – " + day(to, today)
		case !from.IsZero():
			when = "время с " + day(from, today)
		case !to.IsZero():
			when = "время по " + day(to, today)
		default:
			when = "всё время"
		}
	}
	head := "За " + when
	if cat != "" {
		head += " на «" + string(cat) + "»"
	}
	if query != "" {
		head += " по запросу «" + query + "»"
	}
	if count == 0 {
		return head + " трат нет."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s (%s)", head, money(total), plural(count, "трата", "траты", "трат"))
	for i, e := range items {
		if i == maxListed {
			break
		}
		b.WriteString("\n• " + line(e, today))
	}
	if count > min(len(items), maxListed) {
		fmt.Fprintf(&b, "\n…и ещё %d — полный список в панели.", count-min(len(items), maxListed))
	}
	return b.String()
}

func renderChoice(action string, items []domain.Expense, today domain.Date) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Нашёл несколько трат. Какую %s?", action)
	for i, e := range items {
		fmt.Fprintf(&b, "\n%d) %s", i+1, line(e, today))
	}
	b.WriteString("\n" + msgChooseNumber)
	return b.String()
}

// plural: 1 трата, 2 траты, 5 трат.
func plural(n int, one, few, many string) string {
	m10, m100 := n%10, n%100
	word := many
	switch {
	case m10 == 1 && m100 != 11:
		word = one
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		word = few
	}
	return fmt.Sprintf("%d %s", n, word)
}
