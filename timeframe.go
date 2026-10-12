package bytedp_memory

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// timeframeUnits maps unit suffixes (English + Chinese) to durations.
var timeframeUnits = map[string]time.Duration{
	"s": time.Second, "sec": time.Second, "second": time.Second, "seconds": time.Second,
	"m": time.Minute, "min": time.Minute, "minute": time.Minute, "minutes": time.Minute,
	"h": time.Hour, "hr": time.Hour, "hour": time.Hour, "hours": time.Hour,
	"d": 24 * time.Hour, "day": 24 * time.Hour, "days": 24 * time.Hour,
	"w": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour, "weeks": 7 * 24 * time.Hour,
	"mo": 30 * 24 * time.Hour, "month": 30 * 24 * time.Hour, "months": 30 * 24 * time.Hour,
	"y": 365 * 24 * time.Hour, "year": 365 * 24 * time.Hour, "years": 365 * 24 * time.Hour,
	"天": 24 * time.Hour, "周": 7 * 24 * time.Hour, "小时": time.Hour, "分钟": time.Minute,
}

var timeframeNumUnitRe = regexp.MustCompile(`^(\d+)\s*([a-z]+|[\p{Han}]+)$`)

// ParseTimeframe parses a natural-language time window into a duration.
// Supported forms: "7d" / "24h" / "2 days ago" / "last week" / "last month" /
// "today" / "yesterday" / "7天" / "3小时". The returned duration is meant to be
// subtracted from now to build a SearchOptions.AfterDate lower bound.
func ParseTimeframe(s string) (time.Duration, error) {
	orig := strings.TrimSpace(s)
	lower := strings.ToLower(orig)
	if lower == "" {
		return 0, fmt.Errorf("empty timeframe")
	}

	switch lower {
	case "today":
		return 24 * time.Hour, nil
	case "yesterday":
		return 48 * time.Hour, nil
	case "last week":
		return 7 * 24 * time.Hour, nil
	case "last month":
		return 30 * 24 * time.Hour, nil
	case "last year":
		return 365 * 24 * time.Hour, nil
	}

	// "N units ago" → treat as a window of N units.
	if strings.HasSuffix(lower, " ago") {
		lower = strings.TrimSuffix(lower, " ago")
	}

	d, err := parseTimeframeNumUnit(lower)
	if err != nil {
		return 0, fmt.Errorf("unrecognized timeframe %q", orig)
	}
	return d, nil
}

// parseTimeframeNumUnit parses "<number><unit>" forms like "7d" or "2 days".
func parseTimeframeNumUnit(s string) (time.Duration, error) {
	m := timeframeNumUnitRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("not a number+unit form")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, err
	}
	unit, ok := timeframeUnits[m[2]]
	if !ok {
		return 0, fmt.Errorf("unknown unit %q", m[2])
	}
	return time.Duration(n) * unit, nil
}
