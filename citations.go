package bytedp_memory

import (
	"fmt"
	"strings"
)

// formatCitation formats a citation string for a memory entry
// Format: path/to/file.md#L10-L20 (includes end line if available)
func formatCitation(ve *VectorEmbedding) string {
	var parts []string

	// Add file path
	if ve.Metadata.FilePath != "" {
		parts = append(parts, ve.Metadata.FilePath)
	} else if ve.Metadata.SessionKey != "" {
		parts = append(parts, ve.Metadata.SessionKey)
	}

	// Add line numbers
	startLine := ve.Metadata.LineNumber
	endLine := ve.Metadata.EndLineNumber

	if startLine > 0 {
		if endLine > 0 && endLine != startLine {
			parts = append(parts, fmt.Sprintf("L%d-L%d", startLine, endLine))
		} else {
			parts = append(parts, fmt.Sprintf("L%d", startLine))
		}
	}

	if len(parts) == 0 {
		return ve.ID
	}

	return strings.Join(parts, "#")
}
