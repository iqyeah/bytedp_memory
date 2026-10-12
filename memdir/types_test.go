package memdir

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseFrontmatter 覆盖 frontmatter 解析的边界：
// 正常解析、缺少 type、未闭合、超行数、非法值、无 frontmatter。
func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		content string
		max     int
		want    Frontmatter
	}{
		{
			name:    "完整 frontmatter",
			content: "---\nname: 登录门户\ndescription: 联想商城登录入口\ntype: reference\n---\n\n正文内容\n",
			max:     30,
			want:    Frontmatter{Name: "登录门户", Description: "联想商城登录入口", Type: "reference"},
		},
		{
			name:    "无 type 字段",
			content: "---\nname: title\n---\nbody\n",
			max:     30,
			want:    Frontmatter{Name: "title"},
		},
		{
			name:    "未闭合 frontmatter 整体忽略",
			content: "---\nname: title\ndescription: desc\nbody without close\n",
			max:     30,
			want:    Frontmatter{},
		},
		{
			name:    "首行不是 --- 直接返回空",
			content: "# 普通 md\n---\nname: x\n---\n",
			max:     30,
			want:    Frontmatter{},
		},
		{
			name: "超行数：frontmatter 在第 2 行后闭合仍可解析",
			content: "---\n" +
				"name: short\n" +
				"---\n",
			max:  3,
			want: Frontmatter{Name: "short"},
		},
		{
			name: "maxLines 不足：闭合符在窗口外 → 视为未闭合",
			content: "---\n" +
				"name: a\n" +
				"description: b\n" +
				"type: user\n" +
				"---\n",
			max:  3,
			want: Frontmatter{},
		},
		{
			name:    "maxLines<=0 走默认值",
			content: "---\nname: defaulted\ndescription: d\ntype: feedback\n---\n",
			max:     0,
			want:    Frontmatter{Name: "defaulted", Description: "d", Type: "feedback"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFrontmatter(tt.content, tt.max)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseFrontmatter() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestParseFrontmatterEntityMetadata 验证自定义 frontmatter 字段被收集进
// EntityMetadata，标准字段不混入。
func TestParseFrontmatterEntityMetadata(t *testing.T) {
	content := "---\nname: 登录门户\ntype: reference\nstatus: active\nsource: wiki\n---\n"
	fm := ParseFrontmatter(content, FrontmatterMaxLines)
	want := map[string]string{"status": "active", "source": "wiki"}
	if !reflect.DeepEqual(fm.EntityMetadata, want) {
		t.Fatalf("EntityMetadata = %+v, want %+v", fm.EntityMetadata, want)
	}
	if fm.Name != "登录门户" || fm.Type != "reference" {
		t.Fatalf("standard fields misparsed: %+v", fm)
	}
}

// TestParseMemoryKind 覆盖类型枚举解析：合法三类型与非法值。
func TestParseMemoryKind(t *testing.T) {
	for _, k := range []MemoryKind{KindUser, KindFeedback, KindReference} {
		got, ok := ParseMemoryKind(string(k))
		if !ok || got != k {
			t.Errorf("ParseMemoryKind(%q) = (%q, %v), want (%q, true)", k, got, ok, k)
		}
	}
	for _, bad := range []string{"", "project", "fact", "User", "user "} {
		if got, ok := ParseMemoryKind(bad); ok {
			t.Errorf("ParseMemoryKind(%q) = (%q, true), want ok=false", bad, got)
		}
	}
}

// TestFormatMemoryManifest 验证清单格式：kind 标签、年龄、描述、无描述省略。
func TestFormatMemoryManifest(t *testing.T) {
	memories := []MemFile{
		{Filename: "user-goal.md", Kind: KindUser, MtimeMs: 0, Description: "省钱比价"},
		{Filename: "legacy.md", Description: ""},
	}
	ageLabel := func(mtimeMs int64) string {
		if mtimeMs <= 0 {
			return "unknown age"
		}
		return "today"
	}
	got := FormatMemoryManifest(memories, ageLabel)
	if !strings.Contains(got, "[user] user-goal.md (unknown age): 省钱比价") {
		t.Errorf("manifest missing kind line:\n%s", got)
	}
	if !strings.Contains(got, "- legacy.md (unknown age)") {
		t.Errorf("manifest missing no-kind line:\n%s", got)
	}
}

// TestFormatPointerLine 验证指针行格式与 150 字符截断。
func TestFormatPointerLine(t *testing.T) {
	got := FormatPointerLine("登录门户", "memdir/reference-login.md", "联想商城登录入口")
	if got != "- [登录门户](memdir/reference-login.md) — 联想商城登录入口" {
		t.Errorf("FormatPointerLine() = %q", got)
	}

	long := FormatPointerLine("标题", "memdir/x.md", strings.Repeat("长", 200))
	if len([]rune(long)) > 150 {
		t.Errorf("pointer line too long: %d runes", len([]rune(long)))
	}
	if !strings.HasSuffix(long, "…") {
		t.Errorf("truncated pointer line should end with ellipsis: %q", long)
	}
}
