// Package memdir implements the typed long-term memory file layer.
//
// Each durable memory is a Markdown file with frontmatter
// (name / description / type) under the memory directory. Files are the
// source of truth: human-readable, editable, and git-versionable. The
// SQLite vector/FTS store in the parent package acts as a search index.
package memdir

import "strings"

// MemoryKind is the closed taxonomy of long-term memory types.
type MemoryKind string

const (
	// KindUser stores durable knowledge about the user's role,
	// preferences, goals, and focus areas.
	KindUser MemoryKind = "user"
	// KindFeedback stores corrections AND confirmations the user gives
	// about how to approach work.
	KindFeedback MemoryKind = "feedback"
	// KindReference stores pointers to external systems
	// (login portals, dashboards, data sources).
	KindReference MemoryKind = "reference"
)

// ParseMemoryKind parses a raw frontmatter value into a MemoryKind.
// Invalid or missing values return ("", false) — legacy files without a
// type field keep working, files with unknown types degrade gracefully.
func ParseMemoryKind(raw string) (MemoryKind, bool) {
	switch MemoryKind(raw) {
	case KindUser, KindFeedback, KindReference:
		return MemoryKind(raw), true
	}
	return "", false
}

// MemFile is a memory file header parsed from its frontmatter.
type MemFile struct {
	// Filename is the path relative to the memory directory.
	Filename string
	// FilePath is the absolute path.
	FilePath string
	// MtimeMs is the file modification time in milliseconds since epoch.
	MtimeMs int64
	// Description is the one-line frontmatter description used to decide
	// relevance in future conversations.
	Description string
	// Kind is the frontmatter type; empty when the file has no valid type.
	Kind MemoryKind
	// Permalink is the stable identifier for the memory: the frontmatter
	// permalink when present, otherwise the filename.
	Permalink string
}

// Frontmatter is the parsed memory-file frontmatter.
type Frontmatter struct {
	// Name is the memory title.
	Name string
	// Description is the one-line relevance description.
	Description string
	// Type is the raw type value; parse via ParseMemoryKind.
	Type string
	// Permalink is an optional stable identifier (custom field, Claude Code
	// memdir compatible); falls back to the filename when absent.
	Permalink string
	// EntityMetadata holds custom frontmatter keys (any key outside the
	// standard name/description/type/permalink set), for structured metadata
	// filtering (mirrors basic-memory's entity_metadata).
	EntityMetadata map[string]string
}

// FrontmatterMaxLines bounds how many leading lines are inspected for
// frontmatter (mirrors Claude Code's FRONTMATTER_MAX_LINES).
const FrontmatterMaxLines = 30

// ParseFrontmatter extracts name/description/type from raw file content.
// Only the first maxLines lines are inspected; malformed frontmatter
// returns an empty Frontmatter and keeps the file usable.
func ParseFrontmatter(content string, maxLines int) Frontmatter {
	if maxLines <= 0 {
		maxLines = FrontmatterMaxLines
	}

	line := 0
	// Walk lines manually so we can honor maxLines.
	pos := 0
	var lines []string
	for pos < len(content) && len(lines) < maxLines {
		nl := indexByteFrom(content, '\n', pos)
		var s string
		if nl < 0 {
			s = content[pos:]
			pos = len(content)
		} else {
			s = content[pos:nl]
			pos = nl + 1
		}
		lines = append(lines, s)
		line++
	}
	_ = line

	if len(lines) == 0 {
		return Frontmatter{}
	}
	// First line must be the opening delimiter.
	if trimSpace(lines[0]) != "---" {
		return Frontmatter{}
	}

	fm := Frontmatter{}
	closed := false
	for i := 1; i < len(lines); i++ {
		l := trimSpace(lines[i])
		if l == "---" {
			closed = true
			break
		}
		key, val, ok := splitKeyValue(l)
		if !ok {
			continue
		}
		switch key {
		case "name":
			fm.Name = val
		case "description":
			fm.Description = val
		case "type":
			fm.Type = val
		case "permalink":
			fm.Permalink = val
		default:
			if fm.EntityMetadata == nil {
				fm.EntityMetadata = make(map[string]string)
			}
			fm.EntityMetadata[key] = val
		}
	}
	if !closed {
		// Unterminated frontmatter: ignore it entirely.
		return Frontmatter{}
	}
	return fm
}

// ResolvePermalink returns the stable identifier for a memory file: the
// frontmatter permalink when present, otherwise the filename.
func ResolvePermalink(fm Frontmatter, filename string) string {
	if strings.TrimSpace(fm.Permalink) != "" {
		return fm.Permalink
	}
	return filename
}

// FormatMemoryManifest renders memory headers as a text manifest: one line
// per file with [kind] filename (age): description. Used by the recall
// selector prompt.
func FormatMemoryManifest(memories []MemFile, ageLabel func(int64) string) string {
	var b []byte
	for _, m := range memories {
		tag := ""
		if m.Kind != "" {
			tag = "[" + string(m.Kind) + "] "
		}
		age := "unknown age"
		if ageLabel != nil {
			age = ageLabel(m.MtimeMs)
		}
		line := "- " + tag + m.Filename + " (" + age + ")"
		if m.Description != "" {
			line += ": " + m.Description
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	return string(b)
}

// indexByteFrom returns the index of c in s starting at start, or -1.
func indexByteFrom(s string, c byte, start int) int {
	for i := start; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// trimSpace trims ASCII whitespace.
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// splitKeyValue splits "key: value" into its parts, trimming whitespace.
func splitKeyValue(s string) (key, val string, ok bool) {
	idx := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return "", "", false
	}
	key = trimSpace(s[:idx])
	val = trimSpace(s[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, val, true
}
