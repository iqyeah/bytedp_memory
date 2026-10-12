package bytedp_memory

import (
	"path/filepath"
	"testing"
)

// newEntityTestStore opens an in-memory SQLite store for entity tests.
func newEntityTestStore(t *testing.T) *SQLiteStore {
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

// TestUpsertAndListEntities verifies the link channel creates entities and
// that listing orders them by modification time.
func TestUpsertAndListEntities(t *testing.T) {
	store := newEntityTestStore(t)

	if err := store.UpsertEntities([]EntityRecord{
		{Name: "design-spec"},
		{Name: "lenovo"},
	}, 1000); err != nil {
		t.Fatalf("UpsertEntities failed: %v", err)
	}

	entities, err := store.ListEntities(10)
	if err != nil {
		t.Fatalf("ListEntities failed: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(entities))
	}
	names := map[string]bool{}
	for _, e := range entities {
		names[e.Name] = true
		if e.Source != "link" {
			t.Errorf("entity %q source = %q, want link", e.Name, e.Source)
		}
	}
	if !names["design-spec"] || !names["lenovo"] {
		t.Errorf("missing entities in list: %v", names)
	}
}

// TestUpsertEntitiesLLMOverridesLink verifies the dual-channel merge
// semantics: llm enriches an existing link entity, and a later link insert
// never downgrades the enriched data.
func TestUpsertEntitiesLLMOverridesLink(t *testing.T) {
	store := newEntityTestStore(t)

	if err := store.UpsertEntities([]EntityRecord{
		{Name: "lenovo"},
	}, 1000); err != nil {
		t.Fatalf("link upsert failed: %v", err)
	}

	if err := store.UpsertEntities([]EntityRecord{
		{Name: "lenovo", EntityType: "project", Summary: "联想商城项目", Source: "llm"},
	}, 2000); err != nil {
		t.Fatalf("llm upsert failed: %v", err)
	}

	e, err := store.GetEntity("lenovo")
	if err != nil || e == nil {
		t.Fatalf("GetEntity failed: %v (nil=%v)", err, e == nil)
	}
	if e.EntityType != "project" || e.Summary != "联想商城项目" || e.Source != "llm" {
		t.Errorf("llm upsert did not enrich: %+v", e)
	}

	// Link channel again: must not downgrade.
	if err := store.UpsertEntities([]EntityRecord{{Name: "lenovo"}}, 3000); err != nil {
		t.Fatalf("second link upsert failed: %v", err)
	}
	e, _ = store.GetEntity("lenovo")
	if e.EntityType != "project" || e.Summary != "联想商城项目" || e.Source != "llm" {
		t.Errorf("link upsert downgraded llm data: %+v", e)
	}
}

// TestGetAndDeleteEntity verifies exact-name lookup and explicit removal.
func TestGetAndDeleteEntity(t *testing.T) {
	store := newEntityTestStore(t)

	store.UpsertEntities([]EntityRecord{{Name: "ghost"}}, 1000)

	if e, err := store.GetEntity("ghost"); err != nil || e == nil {
		t.Fatalf("GetEntity should find ghost: %v", err)
	}
	if e, err := store.GetEntity("absent"); err != nil || e != nil {
		t.Fatalf("GetEntity should return nil for absent: %v", err)
	}

	if err := store.DeleteEntity("ghost"); err != nil {
		t.Fatalf("DeleteEntity failed: %v", err)
	}
	if e, _ := store.GetEntity("ghost"); e != nil {
		t.Errorf("entity still present after delete: %+v", e)
	}
}

// TestListEntityRelationsIncoming verifies the entity page query returns
// relations pointing at the entity.
func TestListEntityRelationsIncoming(t *testing.T) {
	store := newEntityTestStore(t)

	if err := store.SaveRelations("/m/a.md", "A", []RelationRecord{
		{Type: "implements", Target: "design-spec"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations failed: %v", err)
	}

	rows, err := store.ListEntityRelations("design-spec", true)
	if err != nil {
		t.Fatalf("ListEntityRelations failed: %v", err)
	}
	if len(rows) != 1 || rows[0].Type != "implements" || rows[0].FilePath != "/m/a.md" {
		t.Errorf("unexpected incoming rows: %+v", rows)
	}

	// Outgoing: matched by source permalink.
	rows, err = store.ListEntityRelations("A", false)
	if err != nil {
		t.Fatalf("ListEntityRelations outgoing failed: %v", err)
	}
	if len(rows) != 1 || rows[0].Target != "design-spec" {
		t.Errorf("unexpected outgoing rows: %+v", rows)
	}
}
