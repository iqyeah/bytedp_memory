package memdir

import (
	"strings"
	"testing"
	"time"
)

func msAgo(days int) int64 {
	return time.Now().AddDate(0, 0, -days).UnixMilli()
}

// TestMemoryAgeDays 覆盖天龄计算边界：今天/昨天/N 天/未来时间钳位。
func TestMemoryAgeDays(t *testing.T) {
	tests := []struct {
		name string
		ms   int64
		want int
	}{
		{"今天", time.Now().UnixMilli(), 0},
		{"昨天", msAgo(1), 1},
		{"三天前", msAgo(3), 3},
		{"未来时间钳位为 0", time.Now().Add(48 * time.Hour).UnixMilli(), 0},
		{"非法输入钳位为 0", 0, 0},
		{"负值钳位为 0", -100, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MemoryAgeDays(tt.ms); got != tt.want {
				t.Errorf("MemoryAgeDays() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestMemoryAgeLabel 验证人话标签：today/yesterday/N days ago。
func TestMemoryAgeLabel(t *testing.T) {
	if got := MemoryAgeLabel(time.Now().UnixMilli()); got != "today" {
		t.Errorf("today label = %q", got)
	}
	if got := MemoryAgeLabel(msAgo(1)); got != "yesterday" {
		t.Errorf("yesterday label = %q", got)
	}
	if got := MemoryAgeLabel(msAgo(5)); got != "5 days ago" {
		t.Errorf("older label = %q", got)
	}
}

// TestFreshnessWarning 验证陈旧警告阈值：≤1 天静默，>1 天附警告。
func TestFreshnessWarning(t *testing.T) {
	for _, fresh := range []int64{time.Now().UnixMilli(), msAgo(1)} {
		if got := FreshnessWarning(fresh); got != "" {
			t.Errorf("FreshnessWarning(fresh) = %q, want empty", got)
		}
	}
	got := FreshnessWarning(msAgo(3))
	if got == "" || !strings.Contains(got, "3 days old") || !strings.Contains(got, "Verify") {
		t.Errorf("FreshnessWarning(3d) = %q", got)
	}
}
