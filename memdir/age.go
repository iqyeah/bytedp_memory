package memdir

import (
	"fmt"
	"time"
)

// DayMs is the number of milliseconds in a day.
const DayMs = int64(24 * 60 * 60 * 1000)

// MemoryAgeDays returns days elapsed since mtimeMs, floor-rounded:
// 0 for today, 1 for yesterday, 2+ for older. Negative inputs (future
// mtime, clock skew) clamp to 0.
func MemoryAgeDays(mtimeMs int64) int {
	if mtimeMs <= 0 {
		return 0
	}
	elapsed := time.Now().UnixMilli() - mtimeMs
	if elapsed < 0 {
		return 0
	}
	return int(elapsed / DayMs)
}

// MemoryAgeLabel returns a human-readable age string. Models are poor at
// date arithmetic — a raw timestamp doesn't trigger staleness reasoning
// the way "N days ago" does.
func MemoryAgeLabel(mtimeMs int64) string {
	d := MemoryAgeDays(mtimeMs)
	switch d {
	case 0:
		return "today"
	case 1:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", d)
	}
}

// FreshnessWarning returns a staleness caveat for memories older than one
// day, or "" for fresh (today/yesterday) memories — warning there is noise.
//
// Memories are point-in-time observations, not live state: claims about
// behavior or locations may be outdated and must be verified against the
// current state before being asserted as fact.
func FreshnessWarning(mtimeMs int64) string {
	d := MemoryAgeDays(mtimeMs)
	if d <= 1 {
		return ""
	}
	return fmt.Sprintf(
		"This memory is %d days old. Memories are point-in-time observations, not live state — claims may be outdated. Verify against the current state before asserting as fact.",
		d,
	)
}
