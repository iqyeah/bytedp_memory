package memdir

import (
	"reflect"
	"testing"
)

func TestParseObservations(t *testing.T) {
	content := "" +
		"正文行\n" +
		"- [preference] prefers dark mode #ui #display\n" +
		"- [x] done checkbox item\n" +
		"- [link](https://example.com)\n" +
		"- [[wiki link]]\n" +
		"- [goal] expand to EU market\n" +
		"普通列表项\n"

	got := ParseObservations(content)
	want := []Observation{
		{Category: "preference", Content: "prefers dark mode", Tags: []string{"ui", "display"}, LineNumber: 2},
		{Category: "goal", Content: "expand to EU market", LineNumber: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseObservations mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestParseObservationsNoHeadingRequired(t *testing.T) {
	// 观察行不要求 heading 前置。
	got := ParseObservations("- [fact] 用户住在北京\n")
	if len(got) != 1 || got[0].Category != "fact" || got[0].LineNumber != 1 {
		t.Fatalf("loose mode failed: %+v", got)
	}
}

func TestParseObservationsExcludesNonObservations(t *testing.T) {
	cases := []string{
		"# 标题",
		"- [] 空分类",
		"- [ ] space checkbox",
		"- [X] upper checkbox",
		"- [title](url) markdown link",
		"- [[wikilink]]",
		"- 无分类列表项",
		"- [cat]",
		"#tag-only line",
	}
	for _, in := range cases {
		if got := ParseObservations(in); len(got) != 0 {
			t.Errorf("ParseObservations(%q) should be empty, got %+v", in, got)
		}
	}
}

func TestParseObservationsContext(t *testing.T) {
	content := "" +
		"- [decision] Use bcrypt #security (based on OWASP audit)\n" +
		"- [fact] version 2 (a.k.a. v2)\n" +
		"- [fact] call Foo() to bar\n"

	got := ParseObservations(content)
	want := []Observation{
		{Category: "decision", Content: "Use bcrypt", Tags: []string{"security"}, Context: "based on OWASP audit", LineNumber: 1},
		{Category: "fact", Content: "version 2", Context: "a.k.a. v2", LineNumber: 2},
		// "Foo()" 不是 context：开括号紧贴单词。
		{Category: "fact", Content: "call Foo() to bar", LineNumber: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseObservations context mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestParseFrontmatterPermalink(t *testing.T) {
	content := "---\nname: 用户目标\npermalink: user-goal\ntype: user\n---\n"
	fm := ParseFrontmatter(content, FrontmatterMaxLines)
	if fm.Permalink != "user-goal" {
		t.Fatalf("Permalink = %q, want %q", fm.Permalink, "user-goal")
	}
}

func TestResolvePermalink(t *testing.T) {
	cases := []struct {
		fm       Frontmatter
		filename string
		want     string
	}{
		{Frontmatter{Permalink: "user-goal"}, "memdir/user-目标.md", "user-goal"},
		{Frontmatter{Permalink: "  "}, "memdir/user-目标.md", "memdir/user-目标.md"},
		{Frontmatter{}, "memdir/reference-login.md", "memdir/reference-login.md"},
	}
	for _, c := range cases {
		if got := ResolvePermalink(c.fm, c.filename); got != c.want {
			t.Errorf("ResolvePermalink(%+v, %q) = %q, want %q", c.fm, c.filename, got, c.want)
		}
	}
}
