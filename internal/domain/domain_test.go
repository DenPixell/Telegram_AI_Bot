package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMoney(t *testing.T) {
	ok := map[string]Money{"350": 35000, "350.5": 35050, "350.50": 35050, "0.01": 1, "1000000000": MaxMoney}
	for in, want := range ok {
		got, err := ParseMoney(in)
		if err != nil || got != want {
			t.Errorf("ParseMoney(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "-5", "+5", "1.234", "1.", ".5", "abc", "1,5", "1000000000.01", "1.-5"} {
		if _, err := ParseMoney(in); err == nil {
			t.Errorf("ParseMoney(%q) expected error", in)
		}
	}
	if s := Money(35000).String(); s != "350.00" {
		t.Errorf("String = %q", s)
	}
	b, _ := json.Marshal(Money(7))
	if string(b) != `"0.07"` {
		t.Errorf("Marshal = %s", b)
	}
}

func TestDate(t *testing.T) {
	// 23:30 UTC 15 октября = 02:30 16 октября в Москве.
	ts := time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	msk, _ := time.LoadLocation("Europe/Moscow")
	if d := DateOf(ts, msk); d.String() != "2026-10-16" {
		t.Errorf("DateOf = %s", d)
	}
	d, err := ParseDate("2026-12-31")
	if err != nil || d.AddDays(1).String() != "2027-01-01" {
		t.Errorf("AddDays: %v %v", d, err)
	}
	var j Date
	if err := json.Unmarshal([]byte(`"2026-02-30"`), &j); err == nil {
		t.Error("expected error for invalid date")
	}
}
