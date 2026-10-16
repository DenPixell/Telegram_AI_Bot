package service

import "testing"

func TestNewInviteCode(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		c, err := NewInviteCode()
		if err != nil {
			t.Fatal(err)
		}
		if norm, ok := NormalizeInviteCode(c); !ok || norm != c {
			t.Fatalf("generated code %q is not canonical", c)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q", c)
		}
		seen[c] = true
	}
}

func TestNormalizeInviteCode(t *testing.T) {
	cases := map[string]string{
		"K7M2Q-9XH4T":  "K7M2Q9XH4T",
		"k7m2q 9xh4t":  "K7M2Q9XH4T",
		"OILK7M2Q9X":   "011K7M2Q9X",
		" 0123456789 ": "0123456789",
	}
	for in, want := range cases {
		if got, ok := NormalizeInviteCode(in); !ok || got != want {
			t.Errorf("NormalizeInviteCode(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "K7M2Q", "K7M2Q9XH4TT", "K7M2Q9XH4U", "К7M2Q9XH4T" /* кириллица */} {
		if _, ok := NormalizeInviteCode(in); ok {
			t.Errorf("NormalizeInviteCode(%q) expected not ok", in)
		}
	}
	if got := FormatInviteCode("K7M2Q9XH4T"); got != "K7M2Q-9XH4T" {
		t.Errorf("FormatInviteCode = %q", got)
	}
}
