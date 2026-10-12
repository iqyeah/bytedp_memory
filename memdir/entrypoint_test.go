package memdir

import (
	"strings"
	"testing"
)

// TestTruncateEntrypointNoTruncation 未超限时原样返回（首尾空白被 Trim）。
func TestTruncateEntrypointNoTruncation(t *testing.T) {
	raw := "- [a](memdir/a.md) — hook\n- [b](memdir/b.md) — hook\n"
	got := TruncateEntrypoint(raw)
	if got.WasTruncated || got.Content != strings.TrimSpace(raw) {
		t.Errorf("TruncateEntrypoint() = %+v, want untruncated", got)
	}
	if got.LineCount != 2 {
		t.Errorf("LineCount = %d, want 2", got.LineCount)
	}
}

// TestTruncateEntrypointLines 超行数截断并附行数警告。
func TestTruncateEntrypointLines(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < MaxEntrypointLines+10; i++ {
		sb.WriteString("- line\n")
	}
	got := TruncateEntrypoint(sb.String())
	if !got.WasTruncated || got.Reason != "lines" {
		t.Errorf("WasTruncated=%v Reason=%q, want lines truncation", got.WasTruncated, got.Reason)
	}
	contentLineCount := 0
	for _, l := range strings.Split(got.Content, "\n") {
		if l == "- line" {
			contentLineCount++
		}
	}
	if contentLineCount != MaxEntrypointLines {
		t.Errorf("kept content lines = %d, want %d", contentLineCount, MaxEntrypointLines)
	}
	if !strings.Contains(got.Content, "WARNING") {
		t.Errorf("missing truncation warning:\n%s", got.Content)
	}
}

// TestTruncateEntrypointBytes 超字节截断（单行长行场景）并附字节警告。
func TestTruncateEntrypointBytes(t *testing.T) {
	raw := "- " + strings.Repeat("x", MaxEntrypointBytes+1024) + "\n"
	got := TruncateEntrypoint(raw)
	if !got.WasTruncated || got.Reason != "bytes" {
		t.Errorf("WasTruncated=%v Reason=%q, want bytes truncation", got.WasTruncated, got.Reason)
	}
	if len(got.Content) > MaxEntrypointBytes+256 {
		t.Errorf("content too long: %d bytes", len(got.Content))
	}
	if !strings.Contains(got.Content, "WARNING") {
		t.Errorf("missing truncation warning")
	}
}

// TestTruncateEntrypointEmpty 空输入返回空。
func TestTruncateEntrypointEmpty(t *testing.T) {
	got := TruncateEntrypoint("   \n  ")
	if got.WasTruncated || got.Content != "" {
		t.Errorf("TruncateEntrypoint(empty) = %+v, want empty", got)
	}
}
