package memdir

import (
	"os"
	"path/filepath"
	"sort"
)

// MaxMemoryFiles caps the number of memory files scanned for recall.
// Mirrors Claude Code's MAX_MEMORY_FILES.
const MaxMemoryFiles = 200

// EntrypointName is the MEMORY.md index filename, excluded from scans
// because it holds pointers, not memory content.
const EntrypointName = "MEMORY.md"

// ScanMemoryFiles scans a memory directory recursively for .md files,
// reads their frontmatter, and returns headers sorted newest-first.
//
// Single-pass: each file is stat+read once; results are sorted after the
// read rather than stat-sort-read, halving syscalls for the common case.
// Any unreadable file is skipped silently.
func ScanMemoryFiles(memoryDir string) ([]MemFile, error) {
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var headers []MemFile
	err = walkDir(memoryDir, entries, "", &headers)
	if err != nil {
		return nil, err
	}

	// Newest first, capped at MaxMemoryFiles.
	sort.Slice(headers, func(i, j int) bool {
		return headers[i].MtimeMs > headers[j].MtimeMs
	})
	if len(headers) > MaxMemoryFiles {
		headers = headers[:MaxMemoryFiles]
	}
	return headers, nil
}

// walkDir walks a directory tree collecting memory file headers.
// relative is the path of dir relative to the memory root.
func walkDir(root string, entries []os.DirEntry, relative string, out *[]MemFile) error {
	for _, e := range entries {
		name := e.Name()
		rel := name
		if relative != "" {
			rel = filepath.Join(relative, name)
		}

		switch {
		case e.IsDir():
			subEntries, err := os.ReadDir(filepath.Join(root, rel))
			if err != nil {
				continue
			}
			if err := walkDir(root, subEntries, rel, out); err != nil {
				return err
			}
		case filepath.Ext(name) == ".md" && name != EntrypointName:
			header, ok := scanFile(root, rel)
			if ok {
				*out = append(*out, header)
			}
		}
	}
	return nil
}

// scanFile reads one memory file's frontmatter and builds a MemFile header.
func scanFile(root, relative string) (MemFile, bool) {
	full := filepath.Join(root, relative)
	info, err := os.Stat(full)
	if err != nil {
		return MemFile{}, false
	}

	content, err := readFirstNLines(full, FrontmatterMaxLines)
	if err != nil {
		// Fall back to a header without frontmatter.
		return MemFile{
			Filename:  relative,
			FilePath:  full,
			MtimeMs:   info.ModTime().UnixMilli(),
			Permalink: relative,
		}, true
	}

	fm := ParseFrontmatter(content, FrontmatterMaxLines)
	kind, _ := ParseMemoryKind(fm.Type)
	return MemFile{
		Filename:    relative,
		FilePath:    full,
		MtimeMs:     info.ModTime().UnixMilli(),
		Description: fm.Description,
		Kind:        kind,
		Permalink:   ResolvePermalink(fm, relative),
	}, true
}

// readFirstNLines reads at most maxLines leading lines of a file.
func readFirstNLines(path string, maxLines int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(data)
	lines := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines++
			if lines >= maxLines {
				return s[:i+1], nil
			}
		}
	}
	return s, nil
}
