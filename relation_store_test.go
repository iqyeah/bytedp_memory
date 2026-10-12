package memory

import (
	"path/filepath"
	"testing"
)

// newTestStore opens an in-memory SQLite store with FTS enabled for
// relation tests. Returns the store and a cleanup func.
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	cfg := DefaultStoreConfig(filepath.Join(t.TempDir(), "memory.db"), nil)
	cfg.EnableVectorSearch = false
	store, err := NewSQLiteStore(cfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// addTestRow inserts a memory row with the given file metadata. Mirrors
// the indexer's baseMeta: name and permalink are published into
// entity_metadata (the only persisted metadata channel).
func addTestRow(t *testing.T, store *SQLiteStore, text, filePath, permalink, name string) {
	t.Helper()
	meta := MemoryMetadata{
		FilePath:  filePath,
		Permalink: permalink,
	}
	if name != "" || permalink != "" {
		meta.EntityMetadata = map[string]string{"name": name, "permalink": permalink}
	}
	if err := store.Add(&VectorEmbedding{
		Text:     text,
		Source:   MemorySourceLongTerm,
		Type:     MemoryTypeUser,
		Metadata: meta,
	}); err != nil {
		t.Fatalf("failed to add test row: %v", err)
	}
}

func TestSaveAndListRelations(t *testing.T) {
	store := newTestStore(t)
	fileA := "/memdir/a.md"

	rels := []RelationRecord{
		{Type: "implements", Target: "design-spec", Context: "v2", LineNumber: 10},
		{Type: "relates_to", Target: "user-preferences", LineNumber: 12},
	}
	if err := store.SaveRelations(fileA, "a", rels, 1000); err != nil {
		t.Fatalf("SaveRelations failed: %v", err)
	}

	out, err := store.ListOutgoing(fileA)
	if err != nil {
		t.Fatalf("ListOutgoing failed: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 outgoing relations, got %d", len(out))
	}
	if out[0].Target != "design-spec" && out[1].Target != "design-spec" {
		t.Fatalf("expected design-spec relation, got %+v", out)
	}

	// Re-saving with the same key upserts instead of duplicating.
	if err := store.SaveRelations(fileA, "a", rels, 2000); err != nil {
		t.Fatalf("re-SaveRelations failed: %v", err)
	}
	out, _ = store.ListOutgoing(fileA)
	if len(out) != 2 {
		t.Fatalf("expected 2 relations after upsert, got %d", len(out))
	}

	if err := store.DeleteRelationsByFile(fileA); err != nil {
		t.Fatalf("DeleteRelationsByFile failed: %v", err)
	}
	out, _ = store.ListOutgoing(fileA)
	if len(out) != 0 {
		t.Fatalf("expected 0 relations after delete, got %d", len(out))
	}
}

func TestListIncomingResolvesPermalinkAndName(t *testing.T) {
	store := newTestStore(t)
	fileA := "/memdir/design-spec.md"
	fileB := "/memdir/user-notes.md"

	// Index the target memory with permalink "design-spec" and name
	// "Design Spec", mirroring indexer baseMeta (name in entity_metadata).
	addTestRow(t, store, "Design specification document", fileA, "design-spec", "Design Spec")

	// One relation written by permalink, one by name.
	if err := store.SaveRelations(fileA, "design-spec", []RelationRecord{
		{Type: "implements", Target: "design-spec"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations A failed: %v", err)
	}
	if err := store.SaveRelations(fileB, "user-notes", []RelationRecord{
		{Type: "relates_to", Target: "Design Spec"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations B failed: %v", err)
	}

	for _, query := range []string{"design-spec", "Design Spec"} {
		in, err := store.ListIncoming(query)
		if err != nil {
			t.Fatalf("ListIncoming(%q) failed: %v", query, err)
		}
		if len(in) != 2 {
			t.Fatalf("ListIncoming(%q): expected 2 relations, got %d (%+v)", query, len(in), in)
		}
	}

	in, err := store.ListIncoming("nonexistent")
	if err != nil {
		t.Fatalf("ListIncoming(nonexistent) failed: %v", err)
	}
	if len(in) != 0 {
		t.Fatalf("expected 0 relations for unknown target, got %d", len(in))
	}
}

func TestListIncomingUnresolvableMatchesRawTarget(t *testing.T) {
	store := newTestStore(t)
	fileA := "/memdir/a.md"

	// Dangling relation: target matches no indexed memory, so only the raw
	// text should match.
	if err := store.SaveRelations(fileA, "a", []RelationRecord{
		{Type: "relates_to", Target: "orphan-target"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations failed: %v", err)
	}

	in, err := store.ListIncoming("orphan-target")
	if err != nil {
		t.Fatalf("ListIncoming failed: %v", err)
	}
	if len(in) != 1 || in[0].Type != "relates_to" {
		t.Fatalf("expected 1 raw-target relation, got %+v", in)
	}
}
