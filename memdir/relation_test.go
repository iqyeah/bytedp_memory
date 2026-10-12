package memdir

import (
	"reflect"
	"testing"
)

func TestParseRelationsExplicit(t *testing.T) {
	content := "" +
		"- implements [[Search Design]]\n" +
		"- depends_on [[Database Schema]] (co-founder)\n" +
		"- [[Some Entity]]\n"

	got := ParseRelations(content)
	want := []Relation{
		{Type: "implements", Target: "Search Design", LineNumber: 1},
		{Type: "depends_on", Target: "Database Schema", Context: "co-founder", LineNumber: 2},
		{Type: "relates_to", Target: "Some Entity", LineNumber: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseRelations mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestParseRelationsInline(t *testing.T) {
	content := "This builds on [[Core Design]] and uses [[Utility Functions]].\n"
	got := ParseRelations(content)
	want := []Relation{
		{Type: "links_to", Target: "Core Design", LineNumber: 1},
		{Type: "links_to", Target: "Utility Functions", LineNumber: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseRelations inline mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestParseRelationsExcludesObservations(t *testing.T) {
	// 观察行不是关系；普通文本无 wiki link 也不是。
	content := "" +
		"- [fact] Created Viaweb, the first web app\n" +
		"- [idea] Build a CLI tool #tooling\n" +
		"# 标题\n"

	if got := ParseRelations(content); len(got) != 0 {
		t.Fatalf("ParseRelations should be empty, got %+v", got)
	}
}
