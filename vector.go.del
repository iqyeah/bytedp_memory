package bytedp_memory

import (
	"strings"
	"unicode/utf8"
)

// ChunkText splits text into chunks suitable for embedding
func ChunkText(text string, maxTokens int) []string {
	// Simple chunking by character count
	// A more sophisticated implementation would use a tokenizer
	const approxCharsPerToken = 4
	maxChars := maxTokens * approxCharsPerToken

	if len(text) <= maxChars {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxChars {
			chunks = append(chunks, text)
			break
		}

		// Try to split at a sentence boundary
		splitPos := maxChars
		for i := maxChars; i > maxChars/2; i-- {
			if text[i] == '.' || text[i] == '!' || text[i] == '?' {
				splitPos = i + 1
				break
			}
		}

		// If no sentence boundary found, try to split at a word boundary
		if splitPos == maxChars {
			for i := maxChars; i > maxChars/2; i-- {
				if text[i] == ' ' || text[i] == '\n' {
					splitPos = i + 1
					break
				}
			}
		}

		chunks = append(chunks, text[:splitPos])
		text = text[splitPos:]
	}

	return chunks
}

// ChunkMarkdown splits Markdown text into chunks with structure awareness:
// heading lines act as the only structural boundaries, sections are packed up
// to maxChars, split at line boundaries, and overlong single lines are windowed
// with overlap characters (mirrors basic-memory semantic_chunking.py design).
func ChunkMarkdown(text string, maxChars, overlap int) []string {
	if maxChars <= 0 {
		maxChars = 2000
	}
	if overlap < 0 || overlap >= maxChars {
		overlap = 0
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}

	var chunks []string
	for _, section := range splitMarkdownSections(text) {
		if runeLen(section) <= maxChars {
			if strings.TrimSpace(section) != "" {
				chunks = append(chunks, section)
			}
			continue
		}
		chunks = append(chunks, splitLongSection(section, maxChars, overlap)...)
	}
	return chunks
}

// splitMarkdownSections splits text at ATX heading lines ("# " to "###### ").
// A heading starts a new section; text before the first heading forms its own.
func splitMarkdownSections(text string) []string {
	var sections []string
	var cur strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if isHeadingLine(line) && cur.Len() > 0 {
			sections = append(sections, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		sections = append(sections, cur.String())
	}
	return sections
}

// isHeadingLine reports whether line is an ATX heading (1-6 leading # followed
// by a space). Excludes #tags and horizontal rules.
func isHeadingLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	n := 0
	for n < len(trimmed) && n < 6 && trimmed[n] == '#' {
		n++
	}
	return n > 0 && n < len(trimmed) && trimmed[n] == ' '
}

// splitLongSection packs lines into chunks of at most maxChars, cutting at
// line boundaries. A single overlong line is windowed with overlap.
func splitLongSection(section string, maxChars, overlap int) []string {
	var chunks []string
	var cur strings.Builder
	for _, line := range strings.SplitAfter(section, "\n") {
		if strings.TrimSpace(line) == "" && cur.Len() == 0 {
			continue
		}
		if runeLen(line) > maxChars {
			if cur.Len() > 0 {
				chunks = append(chunks, cur.String())
				cur.Reset()
			}
			chunks = append(chunks, splitByCharWindow(line, maxChars, overlap)...)
			continue
		}
		if runeLen(cur.String())+runeLen(line) > maxChars {
			chunks = append(chunks, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}

// splitByCharWindow windows a single string into maxChars pieces with overlap
// characters between consecutive windows.
func splitByCharWindow(s string, maxChars, overlap int) []string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return []string{s}
	}
	step := maxChars - overlap
	if step <= 0 {
		step = maxChars
	}
	var out []string
	for start := 0; start < len(runes); start += step {
		end := start + maxChars
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return out
}

// runeLen returns the character (not byte) length of s.
func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}
