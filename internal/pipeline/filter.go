package pipeline

import (
	"context"
	"strings"
	"unicode/utf8"

	"expense-bot/internal/domain"
	"expense-bot/internal/metrics"
)

// Verdict - что делать с сообщением после фильтра правил.
type Verdict string

const (
	// VerdictReject - не про траты или запрещённая тема: шаблонный ответ, 0 токенов.
	VerdictReject Verdict = "reject"
	// VerdictNeedAmount - похоже на трату, но без суммы («кофе»): уточнение без LLM.
	VerdictNeedAmount Verdict = "need_amount"
	// VerdictExpense - простая трата, сумма и категория распознаны без LLM.
	VerdictExpense Verdict = "expense"
	// VerdictAgent - запрос, изменение, несколько трат или категория не распознана:
	// в агента (LLM). Запреты к этому моменту уже отфильтрованы.
	VerdictAgent Verdict = "agent"
)

// RejectReason - причина отказа (для метрик и выбора шаблона ответа).
type RejectReason string

const (
	ReasonEmpty    RejectReason = "empty"
	ReasonTooLong  RejectReason = "too_long"
	ReasonStopWord RejectReason = "stop_word"
	ReasonOffTopic RejectReason = "off_topic"
)

// Простая трата: не длиннее 60 символов и 8 слов без суммы и даты.
const (
	simpleMaxRunes = 60
	simpleMaxWords = 8
)

// ParsedExpense - разбор простой траты.
type ParsedExpense struct {
	Amount   domain.Money
	Large    bool        // выше LargeAmount, бот просит подтверждение
	Date     domain.Date // нулевая - сегодня
	Title    string
	Category domain.Category // "" - не распознана
	Source   Source
	Words    []string // слова названия, для обучения кэша по ответу LLM
	Comment  string   // как написал пользователь
}

// Decision - результат анализа сообщения.
type Decision struct {
	Verdict Verdict
	Reason  RejectReason
	Action  ActionKind     // для VerdictAgent: найденное действие над тратами, если есть
	Expense *ParsedExpense // для VerdictExpense и VerdictNeedAmount; для VerdictAgent - подсказка
}

// Analyzer - ступени 0 и 4 конвейера: технические проверки и фильтр правил.
type Analyzer struct {
	dict   *Dictionary
	cat    *Categorizer
	maxLen int
}

func NewAnalyzer(dict *Dictionary, cat *Categorizer, maxLen int) *Analyzer {
	return &Analyzer{dict: dict, cat: cat, maxLen: maxLen}
}

// Analyze классифицирует сообщение. today - текущая дата в поясе пользователя.
func (a *Analyzer) Analyze(ctx context.Context, text string, today domain.Date) Decision {
	d := a.analyze(ctx, text, today)
	metrics.FilterDecisions.Inc(string(d.Verdict), string(d.Reason))
	return d
}

func (a *Analyzer) analyze(ctx context.Context, text string, today domain.Date) Decision {
	text = strings.TrimSpace(text)
	if text == "" {
		return reject(ReasonEmpty)
	}
	if utf8.RuneCountInString(text) > a.maxLen {
		return reject(ReasonTooLong)
	}
	norm := Normalize(text)
	toks := tokenize(norm)
	words := wordsOf(norm)

	if a.hasStopPhrase(words) {
		return reject(ReasonStopWord)
	}
	var action ActionKind
	hard, soft := false, false
	for _, w := range words {
		s := Stem(w)
		if a.dict.hardStop[s] {
			hard = true
		}
		if a.dict.softStop[s] {
			soft = true
		}
		if k, ok := a.dict.actions[s]; ok && action == "" {
			action = k
		}
	}
	if hard {
		return reject(ReasonStopWord)
	}
	known := len(a.dict.Lookup(words)) > 0
	if soft && action == "" && !known {
		return reject(ReasonStopWord)
	}

	amounts := parseAmounts(toks)
	// Запросы, удаление, изменение («последнее было не 350, а 450») - в агента.
	if action != "" {
		return Decision{Verdict: VerdictAgent, Action: action}
	}
	switch {
	case len(amounts) == 0 && known:
		titleWords := a.titleWords(toks, nil, -1)
		g := a.cat.Categorize(ctx, titleWords)
		return Decision{Verdict: VerdictNeedAmount, Expense: &ParsedExpense{
			Title: g.Term.Title, Category: g.Term.Category, Source: g.Source, Words: a.content(titleWords),
		}}
	case len(amounts) == 0:
		return reject(ReasonOffTopic)
	case len(amounts) > 1:
		return Decision{Verdict: VerdictAgent} // «кофе 350 и обед 600»
	}

	amt := amounts[0]
	date, dateIdx, _ := findDate(toks, today, amounts)
	titleWords := a.titleWords(toks, &amt, dateIdx)
	content := a.content(titleWords)
	pe := &ParsedExpense{
		Amount: amt.Value, Large: amt.Large, Date: date, Words: content,
		Comment: truncate(text, domain.MaxCommentLen),
	}
	if len(content) == 0 || len(content) > simpleMaxWords || utf8.RuneCountInString(norm) > simpleMaxRunes {
		return Decision{Verdict: VerdictAgent, Expense: pe}
	}
	g := a.cat.Categorize(ctx, titleWords)
	if !g.Found() {
		return Decision{Verdict: VerdictAgent, Expense: pe} // категорию определит LLM
	}
	pe.Title, pe.Category, pe.Source = g.Term.Title, g.Term.Category, g.Source
	return Decision{Verdict: VerdictExpense, Expense: pe}
}

// titleWords - слова, описывающие трату: без суммы с множителем и валютой и без даты.
// Служебные слова остаются, потому что входят в названия («Вкусно и точка»); словарь
// ищет по всей последовательности, а онлайн-поиск их пропускает.
func (a *Analyzer) titleWords(toks []token, amt *Amount, dateIdx int) []string {
	var out []string
	for i, t := range toks {
		if t.kind != tokWord || i == dateIdx || (amt != nil && i >= amt.first && i <= amt.last) {
			continue
		}
		if weekdayOrRelative(t.text) || (i == dateIdx+1 && months[t.text] != 0) {
			continue // «вчера», «в пятницу», месяц в «5 марта»
		}
		out = append(out, t.text)
	}
	return out
}

// content - слова названия без служебных: «поход в рестик» -> [рестик].
func (a *Analyzer) content(words []string) []string {
	var out []string
	for _, w := range words {
		if !a.dict.fillers[w] {
			out = append(out, w)
		}
	}
	return out
}

func weekdayOrRelative(w string) bool {
	_, rel := relativeDays[w]
	_, wd := weekdays[w]
	return rel || wd
}

func (a *Analyzer) hasStopPhrase(words []string) bool {
	joined := " " + strings.Join(words, " ") + " "
	for _, p := range a.dict.stopPhrases {
		if strings.Contains(joined, " "+p+" ") {
			return true
		}
	}
	return false
}

func reject(r RejectReason) Decision { return Decision{Verdict: VerdictReject, Reason: r} }

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
