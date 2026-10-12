package memdir

import "strings"

// Entrypoint truncation caps, mirroring Claude Code's memdir.ts:
// MEMORY.md is an index, not a memory — keep it concise or it gets cut.
const (
	// MaxEntrypointLines caps the index line count.
	MaxEntrypointLines = 200
	// MaxEntrypointBytes caps the index byte size (long single lines are
	// the failure mode the byte cap targets).
	MaxEntrypointBytes = 25 * 1024
)

// EntrypointResult is the outcome of truncating a MEMORY.md index.
type EntrypointResult struct {
	// Content is the (possibly truncated) index text.
	Content string
	// LineCount is the number of lines in Content.
	LineCount int
	// WasTruncated reports whether either cap fired.
	WasTruncated bool
	// Reason names which cap fired ("lines" or "bytes").
	Reason string
}

// TruncateEntrypoint trims MEMORY.md content to the line and byte caps,
// appending a warning that names which cap fired. Line-truncates first
// (natural boundary), then byte-truncates at the last newline before the
// cap so lines are not cut mid-way.
func TruncateEntrypoint(raw string) EntrypointResult {
	trimmed := strings.TrimSpace(raw)
	contentLines := strings.Split(trimmed, "\n")
	lineCount := len(contentLines)
	byteCount := len(trimmed)

	lineTruncated := lineCount > MaxEntrypointLines
	byteTruncated := byteCount > MaxEntrypointBytes

	if !lineTruncated && !byteTruncated {
		return EntrypointResult{
			Content:   trimmed,
			LineCount: lineCount,
		}
	}

	truncated := trimmed
	if lineTruncated {
		truncated = strings.Join(contentLines[:MaxEntrypointLines], "\n")
	}
	if len(truncated) > MaxEntrypointBytes {
		cutAt := strings.LastIndex(truncated[:MaxEntrypointBytes], "\n")
		if cutAt <= 0 {
			cutAt = MaxEntrypointBytes
		}
		truncated = truncated[:cutAt]
	}

	reason := "lines"
	if byteTruncated {
		reason = "bytes"
	}

	warning := "\n\n> WARNING: " + EntrypointName + " exceeds the " + reason +
		" limit. Only part of it was loaded. Keep index entries to one line under ~150 characters."
	return EntrypointResult{
		Content:      truncated + warning,
		LineCount:    len(strings.Split(truncated, "\n")),
		WasTruncated: true,
		Reason:       reason,
	}
}

// FormatPointerLine renders a one-line index pointer entry for MEMORY.md.
// Format: "- [Title](file.md) — one-line hook". The hook is truncated to
// keep entries under ~150 characters.
func FormatPointerLine(title, relPath, hook string) string {
	line := "- [" + title + "](" + relPath + ")"
	if hook != "" {
		line += " — " + hook
	}
	// Keep the entry to one line, under ~150 runes.
	const maxRunes = 150
	if len([]rune(line)) > maxRunes {
		runes := []rune(line)
		line = string(runes[:maxRunes-1]) + "…"
	}
	return line
}
