package bytedp_memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iqyeah/bytedp_memory/memdir"
)

// Default Markdown chunk budgets for memdir file indexing.
const (
	defaultChunkMaxChars = 2000
	defaultChunkOverlap  = 200
)

// Indexer syncs memdir memory files into a MemoryManager's search index.
// The memdir files are the source of truth; the index is a derived projection
// rebuilt incrementally by content fingerprint — unchanged chunks, observation
// lines and relation lines keep their keys and are never re-embedded.
type Indexer struct {
	mgr           *MemoryManager
	memoryDir     string
	chunkMaxChars int
	chunkOverlap  int
	logf          func(format string, args ...any)
}

// IndexerOption configures an Indexer.
type IndexerOption func(*Indexer)

// WithChunkSize sets the Markdown chunk character budget and overlap.
func WithChunkSize(maxChars, overlap int) IndexerOption {
	return func(ix *Indexer) {
		ix.chunkMaxChars = maxChars
		ix.chunkOverlap = overlap
	}
}

// WithLogf sets the warning logger; the default is silent.
func WithLogf(fn func(format string, args ...any)) IndexerOption {
	return func(ix *Indexer) { ix.logf = fn }
}

// NewIndexer creates an Indexer that reads memdir files under memoryDir (the
// directory containing the "memdir" subdirectory) and indexes them into mgr.
func NewIndexer(mgr *MemoryManager, memoryDir string, opts ...IndexerOption) *Indexer {
	ix := &Indexer{
		mgr:           mgr,
		memoryDir:     memoryDir,
		chunkMaxChars: defaultChunkMaxChars,
		chunkOverlap:  defaultChunkOverlap,
		logf:          func(string, ...any) {},
	}
	for _, o := range opts {
		o(ix)
	}
	return ix
}

// SyncFile indexes a single memdir file. relPath is relative to memoryDir
// (e.g. "memdir/user-foo.md"). The file is read fresh so the fingerprint
// source matches what is on disk; frontmatter supplies kind, description,
// permalink and custom entity metadata.
func (ix *Indexer) SyncFile(ctx context.Context, relPath string) error {
	if ix.mgr == nil {
		return fmt.Errorf("indexer: no memory manager")
	}
	fullPath := filepath.Join(ix.memoryDir, relPath)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("read memory file: %w", err)
	}
	content := string(data)

	info, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("stat memory file: %w", err)
	}

	fm := memdir.ParseFrontmatter(content, memdir.FrontmatterMaxLines)
	kind, _ := memdir.ParseMemoryKind(fm.Type)

	baseMeta := MemoryMetadata{
		FilePath:       fullPath,
		Description:    fm.Description,
		MtimeMs:        info.ModTime().UnixMilli(),
		Permalink:      memdir.ResolvePermalink(fm, relPath),
		EntityMetadata: fm.EntityMetadata,
	}
	// Publish the frontmatter name and resolved permalink into entity
	// metadata so relation-target resolution can match wiki-links written
	// by title as well as permalink (entity metadata is the only metadata
	// channel persisted as JSON on the memories row).
	if fm.Name != "" || baseMeta.Permalink != "" {
		if baseMeta.EntityMetadata == nil {
			baseMeta.EntityMetadata = make(map[string]string)
		}
		if fm.Name != "" {
			baseMeta.EntityMetadata["name"] = fm.Name
		}
		if baseMeta.Permalink != "" {
			baseMeta.EntityMetadata["permalink"] = baseMeta.Permalink
		}
	}
	memType := MemoryType(kind)

	existing, err := ix.mgr.List(ctx, func(ve *VectorEmbedding) bool {
		return ve.Metadata.FilePath == fullPath
	})
	if err != nil {
		return fmt.Errorf("list existing index rows: %w", err)
	}
	existingKeys := make(map[string]string, len(existing))
	for _, ve := range existing {
		key := ve.Metadata.ObservationKey
		if key == "" {
			// Legacy whole-file rows have no ObservationKey; recover the
			// fingerprint from the stored text.
			key = "chunk:" + contentHash(ve.Text)
		}
		existingKeys[key] = ve.ID
	}
	keptKeys := make(map[string]bool)

	// 1) Markdown chunks.
	for _, chunk := range ChunkMarkdown(content, ix.chunkMaxChars, ix.chunkOverlap) {
		key := "chunk:" + contentHash(chunk)
		keptKeys[key] = true
		if _, dup := existingKeys[key]; dup {
			continue
		}
		meta := baseMeta
		meta.ObservationKey = key
		if _, err := ix.mgr.AddMemory(ctx, chunk, MemorySourceLongTerm, memType, meta); err != nil {
			ix.warnf("index chunk: %v", err)
		}
	}

	// 2) Observation lines.
	for _, obs := range memdir.ParseObservations(content) {
		key := "obs:" + contentHash(obs.Category, obs.Content)
		keptKeys[key] = true
		if _, dup := existingKeys[key]; dup {
			continue
		}
		meta := baseMeta
		meta.LineNumber = obs.LineNumber
		meta.ObservationKey = key
		text := fmt.Sprintf("[%s] %s", obs.Category, obs.Content)
		if obs.Context != "" {
			text += " (" + obs.Context + ")"
		}
		if _, err := ix.mgr.AddMemory(ctx, text, MemorySourceLongTerm, memType, meta); err != nil {
			ix.warnf("index observation: %v", err)
		}
	}

	// 3) Relation lines: keep the flat FTS text row for keyword recall and
	// mirror the same relations into the structured relations table for
	// graph-style queries (incoming/outgoing, target resolution).
	relationRows := make([]RelationRecord, 0, 8)
	for _, rel := range memdir.ParseRelations(content) {
		key := "rel:" + contentHash(rel.Type, rel.Target, rel.Context)
		keptKeys[key] = true
		if _, dup := existingKeys[key]; dup {
			relationRows = append(relationRows, RelationRecord{
				Type: rel.Type, Target: rel.Target, Context: rel.Context,
				LineNumber: rel.LineNumber,
			})
			continue
		}
		meta := baseMeta
		meta.LineNumber = rel.LineNumber
		meta.ObservationKey = key
		text := rel.Type + " -> " + rel.Target
		if rel.Context != "" {
			text += " (" + rel.Context + ")"
		}
		if _, err := ix.mgr.AddMemory(ctx, text, MemorySourceLongTerm, memType, meta); err != nil {
			ix.warnf("index relation: %v", err)
		}
		relationRows = append(relationRows, RelationRecord{
			Type: rel.Type, Target: rel.Target, Context: rel.Context,
			LineNumber: rel.LineNumber,
		})
	}
	// Structured projection: relation rows carry no embedding cost, so
	// rebuild them wholesale (delete + insert) instead of diffing. Entities
	// are created the same way: every relation target becomes a graph node
	// and the file itself becomes one too (by name and permalink).
	if store, ok := ix.mgr.store.(*SQLiteStore); ok {
		if err := store.DeleteRelationsByFile(fullPath); err != nil {
			ix.warnf("delete relations: %v", err)
		}
		if err := store.SaveRelations(fullPath, baseMeta.Permalink, relationRows, info.ModTime().UnixMilli()); err != nil {
			ix.warnf("save relations: %v", err)
		}
		entityRows := make([]EntityRecord, 0, len(relationRows)+2)
		for _, r := range relationRows {
			entityRows = append(entityRows, EntityRecord{Name: r.Target, Source: "link"})
		}
		if fm.Name != "" {
			entityRows = append(entityRows, EntityRecord{Name: fm.Name, Source: "link"})
		}
		if baseMeta.Permalink != "" {
			entityRows = append(entityRows, EntityRecord{Name: baseMeta.Permalink, Source: "link"})
		}
		if err := store.UpsertEntities(entityRows, info.ModTime().UnixMilli()); err != nil {
			ix.warnf("upsert entities: %v", err)
		}
	}

	// 4) Delete index rows whose content no longer exists in the file.
	for _, id := range staleIndexIDs(existingKeys, keptKeys) {
		if err := ix.mgr.Delete(ctx, id); err != nil {
			ix.warnf("delete stale index row: %v", err)
		}
	}

	return nil
}

func (ix *Indexer) warnf(format string, args ...any) {
	if ix.logf != nil {
		ix.logf(format, args...)
	}
}

// SyncAll indexes every memdir memory file under memoryDir, removes index
// rows (and relation rows) for files that no longer exist, and records a
// scan watermark in the store meta table.
func (ix *Indexer) SyncAll(ctx context.Context) error {
	if ix.mgr == nil {
		return fmt.Errorf("indexer: no memory manager")
	}
	// Walk the tree directly (not via ScanMemoryFiles, which caps at
	// MaxMemoryFiles for recall manifests) so full syncs cover every file.
	var relPaths []string
	err := filepath.WalkDir(ix.memoryDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(d.Name()) != ".md" || d.Name() == memdir.EntrypointName {
			return nil
		}
		rel, rerr := filepath.Rel(ix.memoryDir, path)
		if rerr != nil {
			return rerr
		}
		relPaths = append(relPaths, rel)
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan memory files: %w", err)
	}

	seen := make(map[string]bool, len(relPaths))
	for _, relPath := range relPaths {
		seen[relPath] = true
		if err := ix.SyncFile(ctx, relPath); err != nil {
			return fmt.Errorf("sync memory file %s: %w", relPath, err)
		}
	}

	if err := ix.removeStaleFileRows(ctx, seen); err != nil {
		return fmt.Errorf("cleanup stale file rows: %w", err)
	}

	// Record the scan watermark (unix seconds) for observability.
	if store, ok := ix.mgr.store.(*SQLiteStore); ok {
		store.setMeta("memory_scan_watermark", fmt.Sprintf("%d", time.Now().Unix()))
	}
	return nil
}

// removeStaleFileRows deletes memories (and their relation rows) whose
// file_path no longer appears among the scanned files.
func (ix *Indexer) removeStaleFileRows(ctx context.Context, seen map[string]bool) error {
	prefix := ix.memoryDir + string(filepath.Separator)
	existing, err := ix.mgr.List(ctx, func(ve *VectorEmbedding) bool {
		return ve.Metadata.FilePath != "" && strings.HasPrefix(ve.Metadata.FilePath, prefix)
	})
	if err != nil {
		return err
	}

	staleFiles := make(map[string]bool)
	var staleIDs []string
	for _, ve := range existing {
		rel, rerr := filepath.Rel(ix.memoryDir, ve.Metadata.FilePath)
		if rerr != nil || seen[rel] {
			continue
		}
		staleFiles[ve.Metadata.FilePath] = true
		staleIDs = append(staleIDs, ve.ID)
	}

	for _, id := range staleIDs {
		if err := ix.mgr.Delete(ctx, id); err != nil {
			ix.warnf("delete stale memory row: %v", err)
		}
	}
	if store, ok := ix.mgr.store.(*SQLiteStore); ok {
		for fp := range staleFiles {
			if err := store.DeleteRelationsByFile(fp); err != nil {
				ix.warnf("delete stale relations: %v", err)
			}
		}
	}
	return nil
}

// staleIndexIDs returns IDs of existing rows whose key is absent from kept.
func staleIndexIDs(existingKeys map[string]string, kept map[string]bool) []string {
	var ids []string
	for key, id := range existingKeys {
		if kept[key] {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// contentHash returns a stable SHA-256 hex fingerprint over the given parts,
// NUL-delimited so the key is order-sensitive and unambiguous.
func contentHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
