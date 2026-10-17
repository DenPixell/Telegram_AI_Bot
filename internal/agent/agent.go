// Package agent обрабатывает сообщение активного пользователя: уточнения, фильтр, простая
// трата без LLM, иначе один запрос к LLM с tool_choice=required. Модель только выбирает
// инструменты и аргументы; Go проверяет аргументы, разрешает цели, выполняет операцию и
// формирует ответ по шаблону из фактического результата.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"expense-bot/internal/config"
	"expense-bot/internal/domain"
	"expense-bot/internal/llm"
	"expense-bot/internal/pipeline"
	"expense-bot/internal/service"
)

type Config struct {
	Mode      string // config.AgentModeTools | config.AgentModeJSON
	MaxTokens int
}

type Agent struct {
	llm      llm.Client
	expenses *service.Expenses
	dict     *pipeline.Dictionary
	cat      *pipeline.Categorizer
	analyzer *pipeline.Analyzer
	quota    *pipeline.Quota // nil - без квот
	cfg      Config

	hist    *history
	pending *pendings
	refused *refusals
	locks   *userLocks
	now     func() time.Time
}

func New(client llm.Client, expenses *service.Expenses, dict *pipeline.Dictionary, cat *pipeline.Categorizer,
	analyzer *pipeline.Analyzer, quota *pipeline.Quota, cfg Config) *Agent {
	return &Agent{
		llm: client, expenses: expenses, dict: dict, cat: cat, analyzer: analyzer, quota: quota, cfg: cfg,
		hist: newHistory(), pending: newPendings(), refused: newRefusals(), locks: newUserLocks(), now: time.Now,
	}
}

// Reply - ответ пользователю. UsedLLM - для метрик и тестов («отказ без LLM»).
type Reply struct {
	Text    string
	UsedLLM bool
}

// maxSteps - запрос к LLM и не больше одного повтора с описанием ошибок аргументов.
const maxSteps = 2

// Handle обрабатывает текст активного пользователя. Actor берётся из Telegram-апдейта.
// Ошибка возвращается только при сбое инфраструктуры (БД); пользовательские ситуации -
// это Reply с шаблоном.
func (a *Agent) Handle(ctx context.Context, actor domain.Actor, text string) (Reply, error) {
	unlock := a.locks.lock(actor.UserID)
	defer unlock()

	today, err := a.expenses.Today(ctx, actor.UserID)
	if err != nil {
		return Reply{}, err
	}
	now := a.now()
	if p, ok := a.pending.take(actor.UserID, now); ok {
		if r, handled, err := a.resolvePending(ctx, actor, p, text, today); handled || err != nil {
			return r, err
		}
	}

	d := a.analyzer.Analyze(ctx, text, today)
	switch d.Verdict {
	case pipeline.VerdictReject:
		return Reply{Text: rejectMessage(d.Reason)}, nil
	case pipeline.VerdictNeedAmount:
		pe := d.Expense
		a.pending.set(actor.UserID, pending{kind: pendingAmount, at: now, words: pe.Words,
			expense: domain.Expense{Title: pe.Title, Category: pe.Category, Comment: strings.TrimSpace(text)}})
		return Reply{Text: renderNeedAmount(pe.Title)}, nil
	case pipeline.VerdictExpense:
		pe := d.Expense
		return a.create(ctx, actor, domain.Expense{
			Amount: pe.Amount, Category: pe.Category, Title: pe.Title, SpentAt: pe.Date, Comment: pe.Comment,
		}, today, pe.Large)
	}
	return a.runLLM(ctx, actor, text, d, today)
}

// resolvePending: ответ на уточнение без LLM. handled=false - это новое сообщение,
// ожидание сбрасывается и текст обрабатывается обычным путём.
func (a *Agent) resolvePending(ctx context.Context, actor domain.Actor, p pending, text string, today domain.Date) (Reply, bool, error) {
	norm := strings.TrimRight(pipeline.Normalize(text), ".!")
	if isCancel(norm) {
		return Reply{Text: msgCancelled}, true, nil
	}
	switch p.kind {
	case pendingConfirm:
		if isYes(norm) {
			r, err := a.create(ctx, actor, p.expense, today, false)
			return r, true, err
		}
	case pendingAmount:
		// Ответ - только сумма: «350». Иначе это новая трата или вопрос.
		d := a.analyzer.Analyze(ctx, text, today)
		if d.Verdict == pipeline.VerdictAgent && d.Expense != nil && len(d.Expense.Words) == 0 {
			e := p.expense
			e.Amount, e.SpentAt = d.Expense.Amount, d.Expense.Date
			if !e.Category.Valid() {
				e.Category = domain.CategoryOther
			}
			r, err := a.create(ctx, actor, e, today, d.Expense.Large)
			return r, true, err
		}
	case pendingChoice:
		n, err := strconv.Atoi(norm)
		if err != nil {
			return Reply{}, false, nil
		}
		if n < 1 || n > len(p.ids) {
			a.pending.set(actor.UserID, p) // номер вне списка: ждём дальше
			return Reply{Text: msgChooseNumber}, true, nil
		}
		scope := domain.ScopeOf(actor)
		e, err := a.expenses.Get(ctx, scope, p.ids[n-1])
		if errors.Is(err, domain.ErrNotFound) {
			return Reply{Text: msgNothingFound}, true, nil
		}
		if err != nil {
			return Reply{}, true, err
		}
		r, err := a.apply(ctx, actor, p.action, e, p.changes, today)
		return r, true, err
	}
	return Reply{}, false, nil
}

func isCancel(s string) bool {
	switch s {
	case "отмена", "отменить", "нет", "не надо", "не", "стоп", "cancel", "no":
		return true
	}
	return false
}

func isYes(s string) bool {
	switch s {
	case "да", "ага", "ок", "окей", "записать", "записывай", "подтверждаю", "верно", "yes", "y", "+":
		return true
	}
	return false
}

// create записывает трату; большая сумма сначала требует подтверждения.
func (a *Agent) create(ctx context.Context, actor domain.Actor, e domain.Expense, today domain.Date, needConfirm bool) (Reply, error) {
	r, _, err := a.createExpense(ctx, actor, e, today, needConfirm)
	return r, err
}

// createExpense - как create, но сообщает, записана ли трата (nil - нет: ждём подтверждения
// или данные не прошли валидацию).
func (a *Agent) createExpense(ctx context.Context, actor domain.Actor, e domain.Expense, today domain.Date, needConfirm bool) (Reply, *domain.Expense, error) {
	if needConfirm {
		a.pending.set(actor.UserID, pending{kind: pendingConfirm, at: a.now(), expense: e})
		return Reply{Text: renderConfirmLarge(e, today)}, nil, nil
	}
	res, err := a.expenses.Create(ctx, actor, e)
	if errors.Is(err, domain.ErrValidation) {
		return Reply{Text: validationText(err)}, nil, nil
	}
	if err != nil {
		return Reply{}, nil, err
	}
	return Reply{Text: renderAdded(res, today)}, &res, nil
}

// validationText - пояснение из ошибки валидации сервиса («сумма должна быть от ...»).
func validationText(err error) string {
	if _, after, ok := strings.Cut(err.Error(), domain.ErrValidation.Error()+": "); ok {
		return "Не получилось: " + after + "."
	}
	return msgNotUnderstood
}

// runLLM - ступень 5: один запрос к модели (плюс максимум один повтор при ошибках аргументов).
func (a *Agent) runLLM(ctx context.Context, actor domain.Actor, text string, d pipeline.Decision, today domain.Date) (Reply, error) {
	norm := pipeline.Normalize(text)
	if a.refused.has(norm, a.now()) {
		return Reply{Text: msgOffTopic}, nil
	}
	if a.quota != nil {
		if err := a.quota.Check(ctx, actor.UserID, actor.FamilyID); errors.Is(err, pipeline.ErrQuotaExceeded) {
			slog.InfoContext(ctx, "llm quota exceeded", "user_id", actor.UserID, "err", err)
			return Reply{Text: msgQuota}, nil
		} else if err != nil {
			return Reply{}, err
		}
	}
	ctx = llm.WithCaller(ctx, llm.Caller{UserID: actor.UserID, FamilyID: actor.FamilyID})

	ts := tools()
	sys := systemPrompt(today)
	req := llm.Request{MaxTokens: a.cfg.MaxTokens, Temperature: llm.Float(0)}
	if a.cfg.Mode == config.AgentModeJSON {
		sys += jsonModeSuffix(ts)
		req.JSONMode = true
	} else {
		req.Tools, req.ToolChoice = ts, llm.ToolChoiceRequired
	}
	req.Messages = append([]llm.Message{{Role: llm.RoleSystem, Content: sys}}, a.hist.get(actor.UserID, a.now())...)
	req.Messages = append(req.Messages, llm.Message{Role: llm.RoleUser, Content: text})

	ex := &executor{a: a, actor: actor, text: text, norm: norm, today: today, amounts: pipeline.ParseAmounts(text)}
	ex.date, _ = pipeline.FindDate(text, today)
	var replies []string
	for step := 1; step <= maxSteps; step++ {
		resp, err := a.llm.Complete(ctx, req)
		if err != nil {
			return a.degrade(ctx, actor, d, today, err)
		}
		calls := resp.Message.ToolCalls
		if req.JSONMode {
			calls = parseJSONCalls(resp.Message.Content)
		}
		if len(calls) == 0 {
			replies = []string{msgNotUnderstood}
			break
		}
		results := make([]callResult, 0, len(calls))
		retry := false
		for i, c := range calls {
			if i == maxCallsPerMessage {
				break
			}
			r, err := ex.run(ctx, c)
			if err != nil {
				return Reply{}, err
			}
			retry = retry || r.feedback != ""
			results = append(results, r)
		}
		if retry && step < maxSteps {
			req.Messages = append(req.Messages, feedbackMessages(resp.Message, calls, results, req.JSONMode)...)
			// Успешные вызовы уже выполнены: их ответы сохраняем, модель повторит только ошибочные.
			for _, r := range results {
				if r.feedback == "" {
					replies = append(replies, r.text)
				}
			}
			continue
		}
		for _, r := range results {
			if r.feedback != "" {
				replies = append(replies, msgNotUnderstood)
			} else {
				replies = append(replies, r.text)
			}
		}
		break
	}
	reply := strings.Join(dedupe(replies), "\n")
	a.hist.add(actor.UserID, a.now(),
		llm.Message{Role: llm.RoleUser, Content: text},
		llm.Message{Role: llm.RoleAssistant, Content: reply})
	a.learn(ctx, d, ex.added)
	return Reply{Text: reply, UsedLLM: true}, nil
}

// learn: если словарь и тезаурус не узнали слово, а модель записала трату с категорией,
// запоминаем - в следующий раз обойдёмся без LLM. Только для однозначного случая:
// одно слово названия и одна трата.
func (a *Agent) learn(ctx context.Context, d pipeline.Decision, added []domain.Expense) {
	if d.Expense == nil || d.Expense.Source != pipeline.SourceNone || len(d.Expense.Words) != 1 || len(added) != 1 {
		return
	}
	e := added[0]
	if e.Category == domain.CategoryOther {
		return // «другое» - не знание о слове
	}
	a.cat.Learn(ctx, d.Expense.Words[0], pipeline.Term{Title: e.Title, Category: e.Category})
}

// degrade - LLM недоступна: простую трату записываем в «другое» без модели,
// иначе шаблонный ответ.
func (a *Agent) degrade(ctx context.Context, actor domain.Actor, d pipeline.Decision, today domain.Date, err error) (Reply, error) {
	if errors.Is(err, context.Canceled) {
		return Reply{}, err
	}
	slog.WarnContext(ctx, "llm failed", "err", err)
	if errors.Is(err, llm.ErrBusy) {
		return Reply{Text: msgBusy}, nil
	}
	if pe := d.Expense; pe != nil && pe.Amount > 0 && len(pe.Words) > 0 {
		r, cerr := a.create(ctx, actor, domain.Expense{
			Amount: pe.Amount, Category: domain.CategoryOther, Title: strings.Join(pe.Words, " "),
			SpentAt: pe.Date, Comment: pe.Comment,
		}, today, pe.Large)
		if cerr != nil {
			return Reply{}, cerr
		}
		r.Text += "\nИИ сейчас недоступен, поэтому категория «другое». Исправить: «последнее было в категории еда»."
		return r, nil
	}
	return Reply{Text: msgUnavailable}, nil
}

// feedbackMessages - сообщения для второго шага: ответ модели и результаты вызовов
// с описанием ошибок.
func feedbackMessages(assistant llm.Message, calls []llm.ToolCall, results []callResult, jsonMode bool) []llm.Message {
	if jsonMode {
		var errs []string
		for _, r := range results {
			if r.feedback != "" {
				errs = append(errs, r.feedback)
			}
		}
		return []llm.Message{
			{Role: llm.RoleAssistant, Content: assistant.Content},
			{Role: llm.RoleUser, Content: "Ошибка в аргументах: " + strings.Join(errs, "; ") + ". Повтори только ошибочные вызовы."},
		}
	}
	out := []llm.Message{assistant}
	for i, r := range results {
		content := `{"ok":true}`
		if r.feedback != "" {
			b, _ := json.Marshal(map[string]string{"error": r.feedback})
			content = string(b)
		}
		out = append(out, llm.Message{Role: llm.RoleTool, ToolCallID: calls[i].ID, Content: content})
	}
	return out
}

// parseJSONCalls разбирает {"calls":[{"name":...,"arguments":{...}}]} (AGENT_MODE=json).
func parseJSONCalls(content string) []llm.ToolCall {
	s := strings.TrimSpace(content)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "```json"), "```")
	s = strings.TrimSpace(strings.TrimSuffix(s, "```"))
	var wire struct {
		Calls []struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"calls"`
	}
	if json.Unmarshal([]byte(s), &wire) != nil {
		return nil
	}
	out := make([]llm.ToolCall, 0, len(wire.Calls))
	for i, c := range wire.Calls {
		args := string(c.Arguments)
		if args == "" || args == "null" {
			args = "{}"
		}
		out = append(out, llm.ToolCall{ID: "json_" + strconv.Itoa(i+1), Name: c.Name, Arguments: args})
	}
	return out
}

func dedupe(ss []string) []string {
	seen := map[string]bool{}
	out := ss[:0]
	for _, s := range ss {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// LooksLikeExpense - сообщение похоже на трату с суммой («кофе 350»), а не на вопрос или
// отказ. Нужно до регистрации: такую трату запоминают и записывают после неё.
func (a *Agent) LooksLikeExpense(ctx context.Context, userID int64, text string) bool {
	today, err := a.expenses.Today(ctx, userID)
	if err != nil {
		return false
	}
	d := a.analyzer.Analyze(ctx, text, today)
	switch d.Verdict {
	case pipeline.VerdictExpense:
		return true
	case pipeline.VerdictAgent:
		// Без слов действия («сколько», «удали») и с суммой: «кофе 350 и обед 600», «фигнюшка 500».
		return d.Action == "" && len(pipeline.ParseAmounts(text)) > 0
	}
	return false
}
