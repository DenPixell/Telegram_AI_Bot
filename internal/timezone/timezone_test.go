package timezone

import (
	"testing"
	"time"
	_ "time/tzdata" // одинаковая база поясов на любой машине

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"expense-bot/internal/domain"
)

func TestParse(t *testing.T) {
	cases := []struct{ in, want string }{
		// Города и падежи.
		{"Новосибирск", "Asia/Novosibirsk"},
		{"в Новосибирске", "Asia/Novosibirsk"},
		{"мой часовой пояс Новосибирск", "Asia/Novosibirsk"},
		{"я живу в Перми", "Asia/Yekaterinburg"},
		{"Вологда", "Europe/Moscow"},
		{"Питер!", "Europe/Moscow"},
		{"в Ростове-на-Дону", "Europe/Moscow"},
		{"Нижнем Новгороде", "Europe/Moscow"},
		{"Калининград", "Europe/Kaliningrad"},
		{"almaty", "Asia/Almaty"},
		// Смещения: в диапазоне РФ - российский пояс, иначе Etc/GMT с обратным знаком.
		{"UTC+3", "Europe/Moscow"},
		{"utc +7", "Asia/Novosibirsk"},
		{"+5", "Asia/Yekaterinburg"},
		{"МСК", "Europe/Moscow"},
		{"мск+2", "Asia/Yekaterinburg"},
		{"МСК -1", "Europe/Kaliningrad"},
		{"GMT-5", "Etc/GMT+5"},
		{"пояс -5", "Etc/GMT+5"}, // минус не теряется вместе с префиксом
		{"+5:30", "Asia/Kolkata"},
		{"+14", "Etc/GMT-14"},
		{"utc", "UTC"},
		// IANA в любом регистре - каноническое имя.
		{"Asia/Omsk", "Asia/Omsk"},
		{"america/new_york", "America/New_York"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			z, err := Parse(c.in)
			require.NoError(t, err)
			assert.Equal(t, c.want, z.Name)
		})
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{"", "пояс", "привет", "+15", "+3:70", "+4:15", "5", "Local", "Mars/Olympus", "../etc/passwd"} {
		_, err := Parse(in)
		assert.ErrorIs(t, err, ErrUnknown, in)
	}
}

func TestDescribe(t *testing.T) {
	oct := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	z, _ := Parse("новосибирск")
	assert.Equal(t, "Новосибирск (UTC+7)", z.Describe(oct))
	z, _ = Parse("+5:30")
	assert.Equal(t, "UTC+5:30", z.Describe(oct))
	z, _ = Parse("лондон")
	assert.Equal(t, "Лондон (UTC+1)", z.Describe(oct), "летнее время в октябре")
}

func TestFromLocation(t *testing.T) {
	cases := []struct {
		lat, lon float64
		want     string
	}{
		{55.75, 37.6, "Europe/Moscow"},
		{55.0, 83.0, "Asia/Novosibirsk"},
		{43.1, 131.9, "Asia/Vladivostok"},
		{0, -30, "Etc/GMT+2"}, // океан: пояс по долготе
		{0, 0, "UTC"},
	}
	for _, c := range cases {
		z, err := FromLocation(c.lat, c.lon)
		require.NoError(t, err)
		assert.Equal(t, c.want, z.Name, "%v,%v", c.lat, c.lon)
	}
	_, err := FromLocation(91, 0)
	assert.Error(t, err)
}

func TestResolvePriority(t *testing.T) {
	assert.Equal(t, "Asia/Omsk", Resolve("", "Bad/Zone", "Asia/Omsk").String())
	assert.Equal(t, "UTC", Resolve("Local").String(), "Local зависит от машины, не от пользователя")
}

func TestToday(t *testing.T) {
	nsk, _ := time.LoadLocation("Asia/Novosibirsk")
	late := time.Date(2026, 10, 15, 18, 30, 0, 0, time.UTC) // 01:30 16 октября в Новосибирске
	assert.Equal(t, "2026-10-16", Today(late, nsk).String())
}

func TestPeriodRange(t *testing.T) {
	d := func(s string) domain.Date { v, _ := domain.ParseDate(s); return v }
	cases := []struct {
		p               Period
		today, from, to string
	}{
		{PeriodToday, "2026-10-16", "2026-10-16", "2026-10-16"},
		{PeriodYesterday, "2026-03-01", "2026-02-28", "2026-02-28"},
		{PeriodWeek, "2026-10-16", "2026-10-12", "2026-10-16"}, // неделя с понедельника
		{PeriodWeek, "2026-10-18", "2026-10-12", "2026-10-18"}, // воскресенье
		{PeriodMonth, "2026-10-16", "2026-10-01", "2026-10-16"},
		{PeriodLastMonth, "2026-01-05", "2025-12-01", "2025-12-31"},
		{PeriodLastMonth, "2024-03-31", "2024-02-01", "2024-02-29"},
	}
	for _, c := range cases {
		t.Run(string(c.p)+"@"+c.today, func(t *testing.T) {
			from, to, err := c.p.Range(d(c.today), domain.Date{}, domain.Date{})
			require.NoError(t, err)
			assert.Equal(t, c.from, from.String())
			assert.Equal(t, c.to, to.String())
		})
	}

	from, to, err := PeriodAll.Range(d("2026-10-16"), domain.Date{}, domain.Date{})
	require.NoError(t, err)
	assert.True(t, from.IsZero() && to.IsZero())

	_, _, err = PeriodCustom.Range(d("2026-10-16"), d("2026-10-10"), d("2026-10-01"))
	assert.ErrorIs(t, err, domain.ErrValidation)
	_, _, err = Period("decade").Range(d("2026-10-16"), domain.Date{}, domain.Date{})
	assert.ErrorIs(t, err, domain.ErrValidation)
}
