package agent

import (
	"crypto/sha256"
	"sync"
	"time"

	"expense-bot/internal/domain"
	"expense-bot/internal/llm"
)

// Состояние в памяти: бот работает в одной реплике (long polling). Рестарт теряет только
// незавершённые уточнения и короткую историю - это допустимо.

const (
	historyTTL      = 10 * time.Minute
	historyMax      = 6 // сообщений (пользователь + бот)
	pendingTTL      = 5 * time.Minute
	refusalCacheTTL = 10 * time.Minute
)

// ---------- история диалога для LLM ----------

type historyEntry struct {
	msg llm.Message
	at  time.Time
}

type history struct {
	mu   sync.Mutex
	data map[int64][]historyEntry
}

func newHistory() *history { return &history{data: map[int64][]historyEntry{}} }

// get возвращает свежие сообщения пользователя (не старше historyTTL).
func (h *history) get(userID int64, now time.Time) []llm.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []llm.Message
	var kept []historyEntry
	for _, e := range h.data[userID] {
		if now.Sub(e.at) <= historyTTL {
			kept = append(kept, e)
			out = append(out, e.msg)
		}
	}
	if len(kept) == 0 {
		delete(h.data, userID)
	} else {
		h.data[userID] = kept
	}
	return out
}

func (h *history) add(userID int64, now time.Time, msgs ...llm.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range msgs {
		h.data[userID] = append(h.data[userID], historyEntry{msg: m, at: now})
	}
	if n := len(h.data[userID]); n > historyMax {
		h.data[userID] = h.data[userID][n-historyMax:]
	}
}

// ---------- незавершённые уточнения ----------

type pendingKind int

const (
	pendingAmount  pendingKind = iota + 1 // ждём сумму для «кофе»
	pendingChoice                         // ждём номер из списка кандидатов
	pendingConfirm                        // ждём «да» для большой суммы
)

type pending struct {
	kind    pendingKind
	at      time.Time
	expense domain.Expense // pendingAmount: заготовка; pendingConfirm: трата к записи
	words   []string       // pendingAmount: слова названия для обучения категорий
	action  string         // pendingChoice: toolUpdate | toolDelete
	ids     []int64        // pendingChoice: кандидаты по порядку номеров
	changes updateArgs     // pendingChoice + update: что поменять
}

type pendings struct {
	mu   sync.Mutex
	data map[int64]pending
}

func newPendings() *pendings { return &pendings{data: map[int64]pending{}} }

// take возвращает и удаляет ожидание пользователя, если оно не истекло.
func (p *pendings) take(userID int64, now time.Time) (pending, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.data[userID]
	delete(p.data, userID)
	if !ok || now.Sub(v.at) > pendingTTL {
		return pending{}, false
	}
	return v, true
}

func (p *pendings) set(userID int64, v pending) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data[userID] = v
}

// ---------- кэш отказов ----------

// refusals: повторный спам тем же нерелевантным вопросом не тратит токены.
type refusals struct {
	mu   sync.Mutex
	data map[[32]byte]time.Time
}

func newRefusals() *refusals { return &refusals{data: map[[32]byte]time.Time{}} }

func (r *refusals) has(normText string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.data[sha256.Sum256([]byte(normText))]
	return ok && now.Sub(at) <= refusalCacheTTL
}

func (r *refusals) add(normText string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.data) > 10_000 { // защита памяти: старые записи не нужны
		for k, at := range r.data {
			if now.Sub(at) > refusalCacheTTL {
				delete(r.data, k)
			}
		}
	}
	r.data[sha256.Sum256([]byte(normText))] = now
}

// ---------- сериализация по пользователю ----------

// userLocks: два быстрых сообщения одного пользователя («удали последнее», «измени последнее»)
// обрабатываются по очереди.
type userLocks struct {
	mu    sync.Mutex
	locks map[int64]*userLock
}

type userLock struct {
	mu   sync.Mutex
	refs int
}

func newUserLocks() *userLocks { return &userLocks{locks: map[int64]*userLock{}} }

func (u *userLocks) lock(userID int64) (unlock func()) {
	u.mu.Lock()
	l, ok := u.locks[userID]
	if !ok {
		l = &userLock{}
		u.locks[userID] = l
	}
	l.refs++
	u.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		u.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(u.locks, userID)
		}
		u.mu.Unlock()
	}
}
