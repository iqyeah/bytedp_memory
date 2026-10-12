package memdir

import "strings"

// Relation is a directed link from one memory to another, extracted from a
// memory file's wiki-link syntax. The three forms mirror basic-memory's
// NOTE-FORMAT:
//
//   - implements [[Search Design]]   → explicit, type "implements"
//   - [[Some Entity]]                → bare, type "relates_to"
//     prose mentioning [[X]]           → inline, type "links_to"
type Relation struct {
	// Type is the relation label ("relates_to", "implements", "links_to", …).
	Type string
	// Target is the wiki-link target (title or permalink of the other memory).
	Target string
	// Context is the trailing "(context)" supporting detail, if any.
	Context string
	// LineNumber is the 1-based line number in the source file.
	LineNumber int
}

// ParseRelations extracts relation lines from raw Markdown content. List-item
// relations are explicit ("type [[Target]]" or bare "[[Target]]"); an inline
// "[[Target]]" in a non-list line becomes a "links_to" relation. Observation
// lines ("- [category] ...") are not relations.
func ParseRelations(content string) []Relation {
	var out []Relation
	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		rest, isItem := trimListMarker(line)
		if isItem {
			if rel, ok := parseRelationItem(rest); ok {
				rel.LineNumber = i + 1
				out = append(out, rel)
			}
			continue
		}
		for _, target := range wikiTargets(line) {
			out = append(out, Relation{Type: "links_to", Target: target, LineNumber: i + 1})
		}
	}
	return out
}

// parseRelationItem parses a list-item body as an explicit or bare relation.
func parseRelationItem(rest string) (Relation, bool) {
	// Bare wiki link: "[[Target]]".
	if strings.HasPrefix(rest, "[[") {
		target, _, ok := firstWikiLink(rest)
		if !ok {
			return Relation{}, false
		}
		return Relation{Type: "relates_to", Target: target}, true
	}
	// Observation "[category] ..." is not a relation.
	if strings.HasPrefix(rest, "[") {
		return Relation{}, false
	}
	// Explicit relation: "type [[Target]] (context)".
	idx := strings.Index(rest, "[[")
	if idx < 0 {
		return Relation{}, false
	}
	typ := strings.TrimSpace(rest[:idx])
	if typ == "" {
		typ = "relates_to"
	}
	target, remainder, ok := firstWikiLink(rest[idx:])
	if !ok {
		return Relation{}, false
	}
	return Relation{Type: typ, Target: target, Context: trailingContext(remainder)}, true
}

// firstWikiLink returns the first "[[target]]" target and the text after its
// closing "]]", or ok=false when none is present or the target is empty.
func firstWikiLink(s string) (target, rest string, ok bool) {
	open := strings.Index(s, "[[")
	if open < 0 {
		return "", "", false
	}
	close := strings.Index(s[open+2:], "]]")
	if close < 0 {
		return "", "", false
	}
	target = strings.TrimSpace(s[open+2 : open+2+close])
	rest = s[open+2+close+2:]
	if target == "" {
		return "", "", false
	}
	return target, rest, true
}

// wikiTargets returns every "[[target]]" target in s, in order.
func wikiTargets(s string) []string {
	var out []string
	for {
		target, rest, ok := firstWikiLink(s)
		if !ok {
			return out
		}
		out = append(out, target)
		s = rest
	}
}

// trailingContext peels a "(context)" group from the very end of s, if the
// opening paren is preceded by whitespace. Returns "" otherwise.
func trailingContext(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, ")") {
		return ""
	}
	for i := len(s) - 2; i >= 0; i-- {
		switch s[i] {
		case '(':
			if i > 0 && s[i-1] != ' ' && s[i-1] != '\t' {
				return ""
			}
			return strings.TrimSpace(s[i+1 : len(s)-1])
		case ')':
			return "" // nested parens are not a context
		}
	}
	return ""
}
