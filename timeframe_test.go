package bytedp_memory

import (
	"testing"
	"time"
)

func TestParseTimeframeUnits(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"7d", 7 * 24 * time.Hour},
		{"24h", 24 * time.Hour},
		{"2 days ago", 48 * time.Hour},
		{"last week", 7 * 24 * time.Hour},
		{"last month", 30 * 24 * time.Hour},
		{"last year", 365 * 24 * time.Hour},
		{"today", 24 * time.Hour},
		{"yesterday", 48 * time.Hour},
		{"7天", 7 * 24 * time.Hour},
		{"3小时", 3 * time.Hour},
		{"30 minutes", 30 * time.Minute},
		{"2 weeks", 14 * 24 * time.Hour},
	}
	for _, c := range cases {
		got, err := ParseTimeframe(c.in)
		if err != nil {
			t.Fatalf("ParseTimeframe(%q) error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseTimeframe(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseTimeframeCaseInsensitive(t *testing.T) {
	got, err := ParseTimeframe("2 Days Ago")
	if err != nil {
		t.Fatalf("case-insensitive parse failed: %v", err)
	}
	if got != 48*time.Hour {
		t.Errorf("got %v, want 48h", got)
	}
}

func TestParseTimeframeErrors(t *testing.T) {
	for _, in := range []string{"", "  ", "banana", "5 parsnips", "7fortnights"} {
		if _, err := ParseTimeframe(in); err == nil {
			t.Errorf("ParseTimeframe(%q) should fail, got nil error", in)
		}
	}
}
