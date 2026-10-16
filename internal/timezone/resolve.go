// Package timezone определяет часовой пояс пользователя: город, смещение («МСК+2», «UTC+5»),
// IANA-имя или геолокация. Сервер работает в UTC, пояса применяются только на границах.
package timezone

import "time"

// Resolve возвращает первый валидный пояс по приоритету:
// users.timezone -> families.default_tz -> APP_TZ -> UTC.
func Resolve(names ...string) *time.Location {
	for _, n := range names {
		if n == "" {
			continue
		}
		if loc, err := time.LoadLocation(n); err == nil {
			return loc
		}
	}
	return time.UTC
}

// TODO(этап 3): Parse(text) по встроенной таблице городов и смещений, FromLocation(lat, lon).
