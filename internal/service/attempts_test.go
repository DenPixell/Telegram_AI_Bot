package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAttemptLimiter(t *testing.T) {
	l := newAttemptLimiter(3, time.Hour, time.Hour)
	now := time.Unix(1_800_000_000, 0)

	for range 2 {
		l.Fail(1, now)
	}
	assert.False(t, l.Blocked(1, now), "две ошибки - ещё не блокировка")
	l.Fail(1, now)
	assert.True(t, l.Blocked(1, now))
	assert.False(t, l.Blocked(2, now), "лимит у каждого свой")
	assert.False(t, l.Blocked(1, now.Add(time.Hour+time.Second)), "блокировка снимается через час")

	// Ошибки в разных окнах не складываются.
	l.Fail(3, now)
	l.Fail(3, now)
	l.Fail(3, now.Add(2*time.Hour))
	assert.False(t, l.Blocked(3, now.Add(2*time.Hour)))

	l.Fail(4, now)
	l.Reset(4)
	l.Fail(4, now)
	l.Fail(4, now)
	assert.False(t, l.Blocked(4, now), "успешный вход сбрасывает счётчик")
}
