package service

import (
	"crypto/rand"
	"math/big"
	"strings"
)

// Коды приглашений: Crockford base32 (без I, L, O, U), 10 символов = 50 бит энтропии.
// Один и тот же код используется в deep link (?start=CODE) и при ручном вводе.
const (
	inviteAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	InviteCodeLen  = 10
)

// NewInviteCode генерирует код из crypto/rand. Уникальность обеспечивает UNIQUE (code)
// в БД, вызывающий повторяет генерацию при нарушении ограничения.
func NewInviteCode() (string, error) {
	b := make([]byte, InviteCodeLen)
	max := big.NewInt(int64(len(inviteAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = inviteAlphabet[n.Int64()]
	}
	return string(b), nil
}

// NormalizeInviteCode приводит ручной ввод к каноническому виду: верхний регистр,
// O->0, I/L->1, без пробелов и дефисов. ok=false, если код не может быть валидным.
func NormalizeInviteCode(s string) (code string, ok bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case ' ', '-', '\t':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		if !strings.ContainsRune(inviteAlphabet, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	code = b.String()
	return code, len(code) == InviteCodeLen
}

// FormatInviteCode возвращает вид для показа человеку: K7M2Q-9XH4T.
func FormatInviteCode(code string) string {
	if len(code) != InviteCodeLen {
		return code
	}
	return code[:5] + "-" + code[5:]
}

// TODO(этап 2): InviteService: Create, Revoke, Join (атомарный инкремент used_count),
// лимит неудачных попыток ввода на telegram_id.
