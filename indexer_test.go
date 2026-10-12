package bytedp_memory

import (
	"reflect"
	"testing"
)

func TestContentHashDeterministic(t *testing.T) {
	a := contentHash("preference", "prefers dark mode")
	b := contentHash("preference", "prefers dark mode")
	if a != b {
		t.Fatalf("contentHash not deterministic: %q vs %q", a, b)
	}
	// 参数顺序敏感：不同组合不得碰撞。
	if a == contentHash("prefers dark mode", "preference") {
		t.Fatal("contentHash must be order-sensitive")
	}
	if len(a) != 64 {
		t.Fatalf("SHA-256 hex length = %d, want 64", len(a))
	}
}

func TestStaleIndexIDs(t *testing.T) {
	existing := map[string]string{
		"chunk:aaa": "id-1",
		"chunk:bbb": "id-2",
		"obs:ccc":   "id-3",
	}
	kept := map[string]bool{
		"chunk:aaa": true,
	}
	got := staleIndexIDs(existing, kept)
	// 顺序不定，仅校验集合。
	wantSet := map[string]bool{"id-2": true, "id-3": true}
	if len(got) != len(wantSet) {
		t.Fatalf("staleIndexIDs = %v", got)
	}
	for _, id := range got {
		if !wantSet[id] {
			t.Errorf("unexpected stale id %q", id)
		}
	}
}

func TestStaleIndexIDsKeepsAll(t *testing.T) {
	existing := map[string]string{"chunk:aaa": "id-1"}
	kept := map[string]bool{"chunk:aaa": true}
	if got := staleIndexIDs(existing, kept); !reflect.DeepEqual(got, []string(nil)) {
		t.Fatalf("kept-all case should return nil, got %v", got)
	}
}
