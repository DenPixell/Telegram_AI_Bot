package service

import (
	"sync"
	"time"
)

// Лимит неудачных вводов кода приглашения: 5 в час на telegram_id, затем блокировка на час.
// Без него 50 бит кода всё равно перебирались бы ботнетом.
const (
	maxCodeFailures   = 5
	codeFailureWindow = time.Hour
	codeBlockDuration = time.Hour
)

// attemptLimiter хранит счётчики в памяти. Этого достаточно, потому что бот работает
// в одной реплике (long polling). Рестарт сбрасывает счётчики, но инициировать его
// атакующий не может.
type attemptLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	block    time.Duration
	entries  map[int64]*attemptEntry
	lastScan time.Time
}

type attemptEntry struct {
	windowStart  time.Time
	failures     int
	blockedUntil time.Time
}

func newAttemptLimiter(max int, window, block time.Duration) *attemptLimiter {
	return &attemptLimiter{max: max, window: window, block: block, entries: map[int64]*attemptEntry{}}
}

// Blocked сообщает, заблокирован ли пользователь в момент now.
func (l *attemptLimiter) Blocked(userID int64, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[userID]
	return ok && now.Before(e.blockedUntil)
}

// Fail засчитывает неудачную попытку. На max-й попытке в окне включается блокировка.
func (l *attemptLimiter) Fail(userID int64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	e, ok := l.entries[userID]
	if !ok || now.Sub(e.windowStart) >= l.window {
		e = &attemptEntry{windowStart: now}
		l.entries[userID] = e
	}
	e.failures++
	if e.failures >= l.max {
		e.blockedUntil = now.Add(l.block)
		e.failures = 0
		e.windowStart = now
	}
}

// Reset очищает счётчик после успешного вступления.
func (l *attemptLimiter) Reset(userID int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, userID)
}

// sweep раз в окно удаляет устаревшие записи, чтобы карта не росла бесконечно.
func (l *attemptLimiter) sweep(now time.Time) {
	if now.Sub(l.lastScan) < l.window {
		return
	}
	l.lastScan = now
	for id, e := range l.entries {
		if now.Sub(e.windowStart) >= l.window && !now.Before(e.blockedUntil) {
			delete(l.entries, id)
		}
	}
}
