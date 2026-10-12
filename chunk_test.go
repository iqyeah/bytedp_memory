package bytedp_memory

import (
	"reflect"
	"testing"
)

func TestChunkMarkdownHeadingBoundaries(t *testing.T) {
	text := "Preamble\n# H1\nBody A\nBody B\n# H2\nTail\n"
	got := ChunkMarkdown(text, 2000, 0)
	want := []string{
		"Preamble\n",
		"# H1\nBody A\nBody B\n",
		"# H2\nTail\n",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChunkMarkdown sections mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestChunkMarkdownLineBoundarySplitting(t *testing.T) {
	// 12 runes > maxChars=7 → split at line boundaries, one line per chunk.
	text := "aaa\nbbb\nccc\n"
	got := ChunkMarkdown(text, 7, 0)
	want := []string{"aaa\n", "bbb\n", "ccc\n"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChunkMarkdown line split mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestChunkMarkdownOverlongLineWindow(t *testing.T) {
	// Single 10-rune line with maxChars=4, overlap=1 → windows of 4 with
	// step 3: "abcd", "defg", "ghij".
	got := ChunkMarkdown("abcdefghij", 4, 1)
	want := []string{"abcd", "defg", "ghij"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChunkMarkdown window mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestChunkMarkdownDefaults(t *testing.T) {
	// Empty input.
	if got := ChunkMarkdown("", 2000, 200); got != nil {
		t.Fatalf("empty text should yield nil, got %q", got)
	}
	// maxChars <= 0 defaults to 2000; short text stays whole.
	if got := ChunkMarkdown("short text", 0, 0); !reflect.DeepEqual(got, []string{"short text"}) {
		t.Fatalf("default maxChars should keep short text whole, got %q", got)
	}
	// overlap >= maxChars clamps to 0 (no infinite loop).
	if got := ChunkMarkdown("abcdefghij", 4, 10); !reflect.DeepEqual(got, []string{"abcd", "efgh", "ij"}) {
		t.Fatalf("clamped overlap mismatch, got %q", got)
	}
}
