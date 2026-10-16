// Package timezone определяет часовой пояс пользователя: город, смещение («МСК+2», «UTC+5»),
// IANA-имя или геолокация. Сервер работает в UTC, пояса применяются только на границах.
// Всё офлайн, без LLM и внешних сервисов.
package timezone

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrUnknown - текст не распознан как город, смещение или IANA-имя.
var ErrUnknown = errors.New("timezone not recognized")

// Zone - распознанный пояс. Name сохраняется в users.timezone, Label показывается человеку.
type Zone struct {
	Name     string // IANA: "Asia/Novosibirsk"
	Label    string // «Новосибирск», «UTC+7»
	Location *time.Location
}

// Describe возвращает вид для подтверждения: «Новосибирск (UTC+7)».
func (z Zone) Describe(now time.Time) string {
	off := OffsetLabel(z.Location, now)
	if z.Label == "" || z.Label == off {
		return off
	}
	return z.Label + " (" + off + ")"
}

// OffsetLabel - смещение пояса в момент t: «UTC+3», «UTC+5:30», «UTC-3:30», «UTC».
func OffsetLabel(loc *time.Location, t time.Time) string {
	_, sec := t.In(loc).Zone()
	if sec == 0 {
		return "UTC"
	}
	sign := "+"
	if sec < 0 {
		sign, sec = "-", -sec
	}
	h, m := sec/3600, sec%3600/60
	if m == 0 {
		return fmt.Sprintf("UTC%s%d", sign, h)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, h, m)
}

// Resolve возвращает первый валидный пояс по приоритету:
// users.timezone -> families.default_tz -> APP_TZ -> UTC.
func Resolve(names ...string) *time.Location {
	for _, n := range names {
		if loc, ok := loadIANA(n); ok {
			return loc
		}
	}
	return time.UTC
}

// loadIANA загружает пояс по IANA-имени. Отсекает "" и "Local", которые
// time.LoadLocation принимает, но которые зависят от машины, а не от пользователя.
func loadIANA(name string) (*time.Location, bool) {
	if name == "" || strings.EqualFold(name, "local") {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	return loc, err == nil
}

// zoneFor собирает Zone по IANA-имени из встроенных таблиц.
func zoneFor(name, label string) (Zone, error) {
	loc, ok := loadIANA(name)
	if !ok {
		return Zone{}, fmt.Errorf("load %q: %w", name, ErrUnknown)
	}
	return Zone{Name: name, Label: label, Location: loc}, nil
}
