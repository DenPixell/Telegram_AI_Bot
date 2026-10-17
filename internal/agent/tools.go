package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"expense-bot/internal/domain"
	"expense-bot/internal/llm"
	"expense-bot/internal/pipeline"
	"expense-bot/internal/service"
	"expense-bot/internal/timezone"
)

// maxCallsPerMessage - защита от модели, которая вызывает инструменты в цикле.
const maxCallsPerMessage = 5

// maxCandidates - сколько трат показывать при неоднозначной цели.
const maxCandidates = 5

// callResult - итог одного вызова. feedback != "" - ошибка аргументов, которую можно
// вернуть модели на втором шаге; text - ответ пользователю.
type callResult struct {
	text     string
	feedback string
}

// executor выполняет вызовы инструментов одного сообщения.
type executor struct {
	a       *Agent
	actor   domain.Actor
	text    string
	norm    string
	today   domain.Date
	amounts []domain.Money // суммы, найденные в сообщении кодом на Go
	date    domain.Date    // дата, найденная в сообщении кодом на Go; нулевая - нет
	added   []domain.Expense
}

func (x *executor) run(ctx context.Context, c llm.ToolCall) (callResult, error) {
	switch c.Name {
	case toolAdd:
		return x.add(ctx, c.Arguments)
	case toolList:
		return x.list(ctx, c.Arguments)
	case toolUpdate:
		return x.change(ctx, toolUpdate, c.Arguments)
	case toolDelete:
		return x.change(ctx, toolDelete, c.Arguments)
	case toolClarify:
		var args struct {
			Code string `json:"code"`
		}
		_ = decodeArgs(c.Arguments, &args)
		return callResult{text: clarifyMessage(args.Code)}, nil
	case toolRefuse:
		x.a.refused.add(x.norm, x.a.now())
		return callResult{text: msgOffTopic}, nil
	}
	return callResult{feedback: fmt.Sprintf("неизвестный инструмент %q", c.Name)}, nil
}

// ---------- add_expense ----------

type addArgs struct {
	Amount   json.RawMessage `json:"amount"`
	Category string          `json:"category"`
	Date     string          `json:"date"`
	Title    string          `json:"title"`
	Comment  string          `json:"comment"`
}

func (x *executor) add(ctx context.Context, raw string) (callResult, error) {
	var args addArgs
	if err := decodeArgs(raw, &args); err != nil {
		return callResult{feedback: "аргументы не JSON: " + err.Error()}, nil
	}
	amount, fb, ok := x.amount(args.Amount)
	if !ok {
		if fb == "" {
			return callResult{text: msgNoAmount}, nil
		}
		return callResult{feedback: fb}, nil
	}
	date, err := optDate(args.Date)
	if err != nil {
		return callResult{feedback: "date: " + err.Error()}, nil
	}
	if date.IsZero() {
		// Модель не передала дату, а в тексте она есть («я потратил вчера 550 на шавуху»).
		date = x.date
	}
	e := domain.Expense{
		Amount:   amount,
		Category: domain.ParseCategory(args.Category),
		SpentAt:  date,
		Title:    clip(strings.ToLower(strings.TrimSpace(args.Title)), service.MaxTitleLen),
		Comment:  strings.TrimSpace(x.text),
	}
	// На втором шаге модель может повторить уже выполненный вызов: одна и та же трата
	// в одном сообщении записывается один раз.
	for _, prev := range x.added {
		if prev.Amount == e.Amount && prev.Category == e.Category && prev.Title == e.Title && prev.SpentAt == e.SpentAt {
			return callResult{}, nil
		}
	}
	r, created, err := x.a.createExpense(ctx, x.actor, e, x.today, amount > largeAmount)
	if err != nil {
		return callResult{}, err
	}
	if created != nil {
		x.added = append(x.added, *created)
	}
	return callResult{text: r.Text}, nil
}

// amount разбирает сумму из ответа модели («350», "350.50", 350) и сверяет её с суммами,
// которые нашёл в тексте Go. ok=false и fb="" - в сообщении суммы нет вовсе (уточнение).
func (x *executor) amount(raw json.RawMessage) (m domain.Money, feedback string, ok bool) {
	if len(x.amounts) == 0 {
		return 0, "", false
	}
	s := strings.TrimSpace(string(raw))
	if unq, err := unquote(s); err == nil {
		s = unq
	}
	s = strings.NewReplacer(" ", "", " ", "", "₽", "", "руб", "", ",", ".").Replace(strings.ToLower(s))
	m, err := domain.ParseMoney(s)
	if err != nil {
		return 0, fmt.Sprintf("amount %q: нужна строка вида \"350\" или \"350.50\"", s), false
	}
	if !slices.Contains(x.amounts, m) {
		// Модель выдумала или пересчитала сумму: принимаем только суммы из сообщения.
		return 0, fmt.Sprintf("amount %s нет в сообщении; возможные суммы: %s", m, joinMoney(x.amounts)), false
	}
	return m, "", true
}

// ---------- list_expenses ----------

type listArgs struct {
	Period   string `json:"period"`
	DateFrom string `json:"date_from"`
	DateTo   string `json:"date_to"`
	Category string `json:"category"`
	Query    string `json:"query"`
}

func (x *executor) list(ctx context.Context, raw string) (callResult, error) {
	var args listArgs
	if err := decodeArgs(raw, &args); err != nil {
		return callResult{feedback: "аргументы не JSON: " + err.Error()}, nil
	}
	p := timezone.Period(args.Period)
	if p == "" {
		p = timezone.PeriodMonth
	}
	from, err1 := optDate(args.DateFrom)
	to, err2 := optDate(args.DateTo)
	if err := errors.Join(err1, err2); err != nil {
		return callResult{feedback: "date_from/date_to: " + err.Error()}, nil
	}
	from, to, err := p.Range(x.today, from, to)
	if err != nil {
		return callResult{feedback: err.Error()}, nil
	}
	f := domain.ExpenseFilter{Scope: domain.ScopeOf(x.actor), From: from, To: to, Limit: maxListed}
	var cat domain.Category
	if args.Category != "" {
		cat = domain.Category(args.Category)
		if !cat.Valid() {
			return callResult{feedback: fmt.Sprintf("category %q не из списка", args.Category)}, nil
		}
		f.Categories = []domain.Category{cat}
	}
	if q := strings.TrimSpace(args.Query); q != "" {
		f.Terms = x.a.dict.Expand(q)
	}
	items, total, count, err := x.a.expenses.List(ctx, f)
	if errors.Is(err, domain.ErrValidation) {
		return callResult{text: validationText(err)}, nil
	}
	if err != nil {
		return callResult{}, err
	}
	return callResult{text: renderList(p, from, to, cat, strings.TrimSpace(args.Query), items, total, count, x.today)}, nil
}

// ---------- update_expense / delete_expense ----------

type targetArgs struct {
	Target string `json:"target"`
	Query  string `json:"query"`
	Period string `json:"period"`
}

// updateArgs - разобранные изменения; nil - поле не меняется.
type updateArgs struct {
	Amount   *domain.Money
	Category *domain.Category
	Date     *domain.Date
	Title    *string
	Comment  *string
}

func (u updateArgs) empty() bool {
	return u.Amount == nil && u.Category == nil && u.Date == nil && u.Title == nil && u.Comment == nil
}

func (x *executor) change(ctx context.Context, action, raw string) (callResult, error) {
	var t targetArgs
	if err := decodeArgs(raw, &t); err != nil {
		return callResult{feedback: "аргументы не JSON: " + err.Error()}, nil
	}
	var changes updateArgs
	if action == toolUpdate {
		var fb string
		if changes, fb = x.parseChanges(raw); fb != "" {
			return callResult{feedback: fb}, nil
		}
		if changes.empty() {
			return callResult{feedback: "не указано, что изменить (amount, category, date, title)"}, nil
		}
	}

	candidates, err := x.resolve(ctx, t)
	if errors.Is(err, errBadTarget) {
		return callResult{feedback: err.Error()}, nil
	}
	if err != nil {
		return callResult{}, err
	}
	// «последнее было не 350, а 450»: старая сумма из фразы должна совпасть с целью.
	// Иначе правится не та трата (последней может быть «такси 1200»).
	if old, ok := x.oldAmount(changes); ok {
		var res callResult
		if candidates, res, err = x.matchOldAmount(ctx, candidates, old); err != nil || res.text != "" {
			return res, err
		}
	}
	switch len(candidates) {
	case 0:
		return callResult{text: msgNothingFound}, nil
	case 1:
		r, err := x.a.apply(ctx, x.actor, action, candidates[0], changes, x.today)
		return callResult{text: r.Text}, err
	}
	ids := make([]int64, len(candidates))
	for i, e := range candidates {
		ids[i] = e.ID
	}
	x.a.pending.set(x.actor.UserID, pending{kind: pendingChoice, at: x.a.now(), action: action, ids: ids, changes: changes})
	verb := "удалить"
	if action == toolUpdate {
		verb = "изменить"
	}
	return callResult{text: renderChoice(verb, candidates, x.today)}, nil
}

var errBadTarget = errors.New("target")

// recentForMatch - сколько последних трат просматривать в поиске по старой сумме.
const recentForMatch = 30

// oldAmount: в сообщении ровно две суммы и новая - одна из них, значит вторая - старая
// («не 350, а 450»). Если старой суммы в сообщении нет, сверять не с чем.
func (x *executor) oldAmount(u updateArgs) (domain.Money, bool) {
	if u.Amount == nil || len(x.amounts) != 2 {
		return 0, false
	}
	switch *u.Amount {
	case x.amounts[0]:
		return x.amounts[1], x.amounts[1] != x.amounts[0]
	case x.amounts[1]:
		return x.amounts[0], true
	}
	return 0, false
}

// matchOldAmount оставляет кандидатов с названной старой суммой. Если среди найденных её нет,
// ищет среди последних трат пользователя; не нашёл - объясняет, а не правит наугад.
func (x *executor) matchOldAmount(ctx context.Context, candidates []domain.Expense, old domain.Money) ([]domain.Expense, callResult, error) {
	withAmount := func(items []domain.Expense) []domain.Expense {
		var out []domain.Expense
		for _, e := range items {
			if e.Amount == old {
				out = append(out, e)
			}
		}
		return out
	}
	if m := withAmount(candidates); len(m) > 0 {
		return m, callResult{}, nil
	}
	recent, _, _, err := x.a.expenses.List(ctx, domain.ExpenseFilter{Scope: domain.ScopeOf(x.actor), Limit: recentForMatch})
	if err != nil {
		return nil, callResult{}, err
	}
	if m := withAmount(recent); len(m) > 0 {
		if len(m) > maxCandidates {
			m = m[:maxCandidates]
		}
		return m, callResult{}, nil
	}
	text := fmt.Sprintf("Не нашёл трату на %s.", money(old))
	if len(candidates) == 1 {
		text = fmt.Sprintf("Последняя трата — %s, а не %s, и трату на %s я не нашёл.",
			line(candidates[0], x.today), money(old), money(old))
	}
	return nil, callResult{text: text + " Уточните, какую исправить, например: «кофе было не 350, а 450»."}, nil
}

// resolve находит кандидатов только среди трат самого пользователя (UserScope):
// модель не оперирует идентификаторами вообще.
func (x *executor) resolve(ctx context.Context, t targetArgs) ([]domain.Expense, error) {
	scope := domain.ScopeOf(x.actor)
	query := strings.TrimSpace(t.Query)
	if t.Target == "last" || (t.Target == "search" && query == "" && t.Period == "") || t.Target == "" && query == "" {
		e, err := x.a.expenses.Last(ctx, scope)
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []domain.Expense{e}, nil
	}
	if t.Target != "search" && t.Target != "" {
		return nil, fmt.Errorf("%w: должно быть last или search, получено %q", errBadTarget, t.Target)
	}
	p := timezone.Period(t.Period)
	if p == "" {
		p = timezone.PeriodAll
	}
	from, to, err := p.Range(x.today, domain.Date{}, domain.Date{})
	if err != nil {
		return nil, fmt.Errorf("%w: period: %w", errBadTarget, err)
	}
	f := domain.ExpenseFilter{Scope: scope, From: from, To: to, Limit: maxCandidates}
	if query != "" {
		f.Terms = x.a.dict.Expand(query)
	}
	items, _, _, err := x.a.expenses.List(ctx, f)
	return items, err
}

func (x *executor) parseChanges(raw string) (updateArgs, string) {
	var args struct {
		Amount   json.RawMessage `json:"amount"`
		Category *string         `json:"category"`
		Date     *string         `json:"date"`
		Title    *string         `json:"title"`
		Comment  *string         `json:"comment"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return updateArgs{}, "аргументы не JSON: " + err.Error()
	}
	var u updateArgs
	if s := strings.TrimSpace(string(args.Amount)); s != "" && s != "null" && s != `""` {
		m, fb, ok := x.amount(args.Amount)
		if !ok {
			if fb == "" {
				fb = "суммы в сообщении нет"
			}
			return updateArgs{}, fb
		}
		u.Amount = &m
	}
	if args.Category != nil && *args.Category != "" {
		c := domain.Category(*args.Category)
		if !c.Valid() {
			return updateArgs{}, fmt.Sprintf("category %q не из списка", *args.Category)
		}
		u.Category = &c
	}
	if args.Date != nil && *args.Date != "" {
		d, err := domain.ParseDate(*args.Date)
		if err != nil {
			return updateArgs{}, "date: " + err.Error()
		}
		u.Date = &d
	}
	if args.Title != nil && strings.TrimSpace(*args.Title) != "" {
		t := clip(strings.ToLower(strings.TrimSpace(*args.Title)), service.MaxTitleLen)
		u.Title = &t
	}
	if args.Comment != nil {
		c := strings.TrimSpace(*args.Comment)
		u.Comment = &c
	}
	return u, ""
}

// apply выполняет действие над найденной тратой и рендерит результат.
func (a *Agent) apply(ctx context.Context, actor domain.Actor, action string, e domain.Expense, u updateArgs, today domain.Date) (Reply, error) {
	scope := domain.ScopeOf(actor)
	if action == toolDelete {
		err := a.expenses.Delete(ctx, scope, e.ID)
		if errors.Is(err, domain.ErrNotFound) {
			return Reply{Text: msgNothingFound}, nil
		}
		if err != nil {
			return Reply{}, err
		}
		return Reply{Text: renderDeleted(e, today)}, nil
	}
	after := e
	if u.Amount != nil {
		after.Amount = *u.Amount
	}
	if u.Category != nil {
		after.Category = *u.Category
	}
	if u.Date != nil {
		after.SpentAt = *u.Date
	}
	if u.Title != nil {
		after.Title = *u.Title
	}
	if u.Comment != nil {
		after.Comment = *u.Comment
	}
	res, err := a.expenses.Update(ctx, scope, after)
	switch {
	case errors.Is(err, domain.ErrValidation):
		return Reply{Text: validationText(err)}, nil
	case errors.Is(err, domain.ErrConflict):
		return Reply{Text: "Трату только что изменили в панели. Повторите команду."}, nil
	case errors.Is(err, domain.ErrNotFound):
		return Reply{Text: msgNothingFound}, nil
	case err != nil:
		return Reply{}, err
	}
	return Reply{Text: renderUpdated(e, res, today)}, nil
}

// ---------- разбор аргументов ----------

// decodeArgs: аргументы модели - недоверенный JSON. Лишние поля игнорируются,
// пустая строка считается пустым объектом.
func decodeArgs(raw string, dst any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	return dec.Decode(dst)
}

func optDate(s string) (domain.Date, error) {
	if s = strings.TrimSpace(s); s == "" {
		return domain.Date{}, nil
	}
	return domain.ParseDate(s)
}

func unquote(s string) (string, error) {
	var v string
	err := json.Unmarshal([]byte(s), &v)
	return v, err
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func joinMoney(ms []domain.Money) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = m.String()
	}
	return strings.Join(parts, ", ")
}

// largeAmount - выше этого трата требует подтверждения (как и в простом пути без LLM).
const largeAmount = pipeline.LargeAmount
