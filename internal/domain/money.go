package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// Money - сумма в копейках. Никаких float.
type Money int64

// MaxMoney совпадает с CHECK в таблице expenses (1 млрд рублей).
const MaxMoney Money = 100_000_000_000

func (m Money) Valid() bool { return m > 0 && m <= MaxMoney }

// String возвращает вид для API: "350.00".
func (m Money) String() string {
	sign := ""
	v := int64(m)
	if v < 0 {
		sign, v = "-", -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

// ParseMoney разбирает строгий формат API: "350", "350.5", "350.50".
// Разговорные формы («1.5к», «1 200 р») разбирает pipeline, не этот код.
func ParseMoney(s string) (Money, error) {
	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" || strings.HasPrefix(whole, "-") || strings.HasPrefix(whole, "+") ||
		(hasFrac && (len(frac) == 0 || len(frac) > 2)) {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w > int64(MaxMoney/100) {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	var f int64
	if hasFrac {
		if len(frac) == 1 {
			frac += "0"
		}
		f, err = strconv.ParseInt(frac, 10, 64)
		if err != nil || strings.HasPrefix(frac, "-") || strings.HasPrefix(frac, "+") {
			return 0, fmt.Errorf("invalid amount %q", s)
		}
	}
	if m := Money(w*100 + f); m <= MaxMoney {
		return m, nil
	}
	return 0, fmt.Errorf("amount %q exceeds maximum", s)
}

func (m Money) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(m.String())), nil }

func (m *Money) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return fmt.Errorf("amount must be a string like \"350.00\": %w", err)
	}
	v, err := ParseMoney(s)
	if err != nil {
		return err
	}
	*m = v
	return nil
}
