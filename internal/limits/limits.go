package limits

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrRateLimited   = errors.New("rate limited")
	ErrQuotaExceeded = errors.New("llm quota exceeded")
)

// RateLimiter - скользящее окно сообщений на пользователя (ступень 1). В памяти:
// бот работает в одной реплике.
type RateLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	hits      map[int64][]time.Time
	lastSweep time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, hits: map[int64][]time.Time{}}
}

// Allow засчитывает сообщение и сообщает, укладывается ли пользователь в лимит.
func (l *RateLimiter) Allow(userID int64, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	cutoff := now.Add(-l.window)
	h := l.hits[userID]
	i := 0
	for i < len(h) && !h[i].After(cutoff) {
		i++
	}
	h = h[i:]
	if len(h) >= l.limit {
		l.hits[userID] = h
		return false
	}
	l.hits[userID] = append(h, now)
	return true
}

func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	cutoff := now.Add(-l.window)
	for id, h := range l.hits {
		if len(h) == 0 || !h[len(h)-1].After(cutoff) {
			delete(l.hits, id)
		}
	}
}

// UsageCounter считает запросы к LLM за период (таблица llm_usage).
type UsageCounter interface {
	CountLLMUsage(ctx context.Context, userID, familyID int64, since time.Time) (user, family, total int, err error)
}

// Quota - суточные квоты LLM на пользователя, семью и глобальный предохранитель.
type Quota struct {
	counter                    UsageCounter
	perUser, perFamily, global int
	now                        func() time.Time
}

func NewQuota(counter UsageCounter, perUser, perFamily, global int) *Quota {
	return &Quota{counter: counter, perUser: perUser, perFamily: perFamily, global: global, now: time.Now}
}

// Check возвращает ErrQuotaExceeded, если любой из лимитов за последние 24 часа исчерпан.
func (q *Quota) Check(ctx context.Context, userID, familyID int64) error {
	u, f, total, err := q.counter.CountLLMUsage(ctx, userID, familyID, q.now().Add(-24*time.Hour))
	if err != nil {
		return fmt.Errorf("check quota: %w", err)
	}
	switch {
	case u >= q.perUser:
		return fmt.Errorf("user quota %d: %w", q.perUser, ErrQuotaExceeded)
	case f >= q.perFamily:
		return fmt.Errorf("family quota %d: %w", q.perFamily, ErrQuotaExceeded)
	case q.global > 0 && total >= q.global:
		return fmt.Errorf("global quota %d: %w", q.global, ErrQuotaExceeded)
	}
	return nil
}
