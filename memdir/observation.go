package memdir

import "strings"

// Observation is a single structured fact line extracted from a memory file.
type Observation struct {
	// Category is the bracketed kind prefix (e.g. "preference", "goal").
	Category string
	// Content is the fact text after the category tag.
	Content string
	// Tags are #hashtags found in the line.
	Tags []string
	// Context is the trailing "(context)" supporting detail or source, if any
	// (mirrors basic-memory's observation context).
	Context string
	// LineNumber is the 1-based line number in the source file.
	LineNumber int
}

// ParseObservations extracts observation lines of the form
// "- [category] content #tags (context)" from raw Markdown content.
// No heading is required. Checkbox items, Markdown links, and wiki-links
// are excluded (mirrors basic-memory's entity_parser exclusion rules).
func ParseObservations(content string) []Observation {
	var out []Observation
	lineNo := 0
	for _, raw := range strings.Split(content, "\n") {
		lineNo++
		obs, ok := parseObservationLine(strings.TrimSpace(raw))
		if !ok {
			continue
		}
		obs.LineNumber = lineNo
		out = append(out, obs)
	}
	return out
}

// parseObservationLine parses a single trimmed line; ok=false when the line
// is not an observation (plain text, heading, checkbox, link, etc.).
func parseObservationLine(line string) (Observation, bool) {
	// List item marker: "- " or "* ".
	rest, ok := trimListMarker(line)
	if !ok {
		return Observation{}, false
	}
	// Wiki-link form "[[" is not an observation.
	if strings.HasPrefix(rest, "[[") {
		return Observation{}, false
	}
	// "[category] content" prefix.
	if !strings.HasPrefix(rest, "[") {
		return Observation{}, false
	}
	end := strings.Index(rest, "]")
	if end <= 1 {
		// No closing bracket or empty category.
		return Observation{}, false
	}
	category := rest[1:end]
	// Exclude checkbox items: "[ ]", "[x]", "[X]".
	if category == " " || category == "x" || category == "X" {
		return Observation{}, false
	}
	after := rest[end+1:]
	// Exclude Markdown links: "[title](url)".
	if strings.HasPrefix(after, "(") {
		return Observation{}, false
	}
	content := strings.TrimSpace(after)
	if content == "" {
		return Observation{}, false
	}
	tags, body := extractTags(content)
	if strings.TrimSpace(body) == "" {
		return Observation{}, false
	}
	body, context := splitContext(body)
	return Observation{Category: category, Content: body, Tags: tags, Context: context}, true
}

// splitContext peels a trailing "(context)" group from observation content.
// The opening paren must be preceded by whitespace (or start the line), so a
// function call like "Foo()" is left intact. Unbalanced or empty contexts are
// kept in the content unchanged.
func splitContext(content string) (body, context string) {
	trimmed := strings.TrimRight(content, " \t")
	if !strings.HasSuffix(trimmed, ")") {
		return content, ""
	}
	depth := 0
	for i := len(trimmed) - 1; i >= 0; i-- {
		switch trimmed[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				if i > 0 && trimmed[i-1] != ' ' && trimmed[i-1] != '\t' {
					return content, ""
				}
				ctx := strings.TrimSpace(trimmed[i+1 : len(trimmed)-1])
				b := strings.TrimSpace(trimmed[:i])
				if ctx == "" || b == "" {
					return content, ""
				}
				return b, ctx
			}
		}
	}
	return content, ""
}

// trimListMarker strips a "- " or "* " bullet prefix.
func trimListMarker(s string) (string, bool) {
	if strings.HasPrefix(s, "- ") {
		return s[2:], true
	}
	if strings.HasPrefix(s, "* ") {
		return s[2:], true
	}
	return "", false
}

// extractTags pulls #hashtags out of a line and returns the remaining text.
func extractTags(s string) (tags []string, rest string) {
	fields := strings.Fields(s)
	var body []string
	for _, f := range fields {
		if strings.HasPrefix(f, "#") && len(f) > 1 && !strings.Contains(f, "://") {
			tags = append(tags, strings.TrimPrefix(f, "#"))
			continue
		}
		body = append(body, f)
	}
	return tags, strings.Join(body, " ")
}
