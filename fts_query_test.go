package bytedp_memory

import (
	"strings"
	"testing"
)

func TestSanitizeFTS5QueryBooleanWhitelist(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a b AND c", "a b AND c"},
		{"a OR b NOT c", "a OR b NOT c"},
		{"(a OR b) AND c", "( a OR b ) AND c"},
		// Trailing operator without a right operand is quoted as a literal.
		{"a OR", `a "OR"`},
		// Leading operator is quoted as a literal.
		{"NOT foo", `"NOT" foo`},
		// NEAR is unsupported: quoted as a literal phrase.
		{"a NEAR b", `a "NEAR" b`},
		// Operator symbols are stripped.
		{"price >= 100", "price 100"},
		{"a * b + c - d ^ e ~ f : g = h < i > j", "a b c d e f g h i j"},
		// Too short for trigram matching → empty (caller falls back to LIKE).
		{"ab", ""},
		{"价格", ""},
	}
	for _, c := range cases {
		got := sanitizeFTS5Query(c.in)
		if got != c.want {
			t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildRelaxedFTSQuery(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello", `"hello"*`},
		{"hello world", `"hello" OR "world"`},
		// 带引号的多词短语会被 Fields 拆成两词（实现行为，仅记录）。
		{`"quoted term"`, `"quoted" OR "term"`},
		{"   ", ""},
		{`""`, ""},
	}
	for _, c := range cases {
		got := buildRelaxedFTSQuery(c.in)
		if got != c.want {
			t.Errorf("buildRelaxedFTSQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHasMinimumTrigramQuery(t *testing.T) {
	if hasMinimumTrigramQuery("ab") {
		t.Error("2-rune query should be below trigram minimum")
	}
	if !hasMinimumTrigramQuery("abc") {
		t.Error("3-rune query should satisfy trigram minimum")
	}
	if !hasMinimumTrigramQuery(" 你好吗 ") {
		t.Error("3-rune CJK with padding should satisfy trigram minimum")
	}
}

func TestNormalizeBooleanOperatorsPreservesPhrases(t *testing.T) {
	// Quoted phrase stays intact and is not re-tokenized.
	got := normalizeBooleanOperators(`"hello world" AND foo`)
	if got != `"hello world" AND foo` {
		t.Errorf("normalizeBooleanOperators = %q", got)
	}
	// Parentheses survive as standalone tokens.
	got = normalizeBooleanOperators("(a AND b) OR (c AND d)")
	if got != "( a AND b ) OR ( c AND d )" {
		t.Errorf("normalizeBooleanOperators parens = %q", got)
	}
}

func TestSplitFTSQueryTokens(t *testing.T) {
	got := splitFTSQueryTokens(`"hello world" AND (foo OR bar)`)
	want := []string{`"hello world"`, "AND", "(", "foo", "OR", "bar", ")"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("splitFTSQueryTokens = %q, want %q", got, want)
	}
}
