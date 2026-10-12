package memdir

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestFile 写一个测试记忆文件并设置 mtime（排序验证用）。
func writeTestFile(t *testing.T, dir, name, content string, mtime time.Time) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(full, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// TestScanMemoryFiles 验证扫描：排除 MEMORY.md、递归、mtime 倒序、frontmatter 解析。
func TestScanMemoryFiles(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()
	older := base.Add(-48 * time.Hour)

	writeTestFile(t, dir, "MEMORY.md", "# index", base) // 索引文件必须被排除
	writeTestFile(t, dir, "user-goal.md", "---\nname: 省钱\ndescription: 比价\ntype: user\n---\nbody", base)
	writeTestFile(t, dir, "sub/ref-login.md", "---\nname: 登录\ndescription: 入口\ntype: reference\n---\nbody", older)
	writeTestFile(t, dir, "notes.txt", "not md", base) // 非 .md 忽略

	got, err := ScanMemoryFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("ScanMemoryFiles() len = %d, want 2: %+v", len(got), got)
	}
	// 倒序：新文件在前
	if got[0].Filename != "user-goal.md" || got[1].Filename != filepath.Join("sub", "ref-login.md") {
		t.Errorf("unexpected order: %+v", got)
	}
	if got[0].Kind != KindUser || got[0].Description != "比价" {
		t.Errorf("frontmatter not parsed: %+v", got[0])
	}
	if got[1].Kind != KindReference {
		t.Errorf("subdir frontmatter not parsed: %+v", got[1])
	}
}

// TestScanMemoryFilesNotExist 目录不存在时静默返回空。
func TestScanMemoryFilesNotExist(t *testing.T) {
	got, err := ScanMemoryFiles(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing dir, got %+v", got)
	}
}

// TestScanMemoryFilesCap 验证 200 文件上限（超出截断，保留最新）。
func TestScanMemoryFilesCap(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()
	for i := 0; i < MaxMemoryFiles+20; i++ {
		mtime := base.Add(-time.Duration(i) * time.Minute)
		name := "m" + string(rune('a'+i%26)) + "_" + padInt(i) + ".md"
		writeTestFile(t, dir, name, "---\nname: n\ntype: user\n---\n", mtime)
	}

	got, err := ScanMemoryFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxMemoryFiles {
		t.Errorf("len = %d, want capped at %d", len(got), MaxMemoryFiles)
	}
	// 最新的保留（i=0 最旧被裁掉，i=MaxMemoryFiles+19 最新）
	for i := 1; i < len(got); i++ {
		if got[i-1].MtimeMs < got[i].MtimeMs {
			t.Errorf("not sorted newest-first at %d: %+v vs %+v", i, got[i-1], got[i])
		}
	}
}

func padInt(i int) string {
	s := ""
	for n := i; n > 0 || len(s) < 3; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
