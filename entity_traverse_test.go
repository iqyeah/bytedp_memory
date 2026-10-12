package memory

import "testing"

// TestTraverseHops walks upstream along a chain A→B, B→C starting from C:
// one hop returns the B→C edge, two hops also return the A→B edge.
func TestTraverseHops(t *testing.T) {
	store := newEntityTestStore(t)

	if err := store.SaveRelations("/m/a.md", "A", []RelationRecord{
		{Type: "relates_to", Target: "B"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations A failed: %v", err)
	}
	if err := store.SaveRelations("/m/b.md", "B", []RelationRecord{
		{Type: "relates_to", Target: "C"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations B failed: %v", err)
	}

	edges, err := store.Traverse("C", 2, false)
	if err != nil {
		t.Fatalf("Traverse failed: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d: %+v", len(edges), edges)
	}
	targets := map[string]bool{}
	for _, e := range edges {
		targets[e.Target] = true
	}
	if !targets["B"] || !targets["C"] {
		t.Errorf("expected targets B and C, got %v", targets)
	}
}

// TestTraverseOutgoingOnly follows only out-edges from the start entity.
func TestTraverseOutgoingOnly(t *testing.T) {
	store := newEntityTestStore(t)

	if err := store.SaveRelations("/m/b.md", "B", []RelationRecord{
		{Type: "relates_to", Target: "C"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations failed: %v", err)
	}
	// An unrelated in-edge pointing at B must not be followed.
	if err := store.SaveRelations("/m/x.md", "X", []RelationRecord{
		{Type: "relates_to", Target: "B"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations X failed: %v", err)
	}

	edges, err := store.Traverse("B", 2, true)
	if err != nil {
		t.Fatalf("Traverse failed: %v", err)
	}
	if len(edges) != 1 || edges[0].Target != "C" {
		t.Errorf("outgoing-only traverse should return only the B→C edge: %+v", edges)
	}
}

// TestTraverseDedup verifies the same edge is never returned twice even
// when it is reachable through several paths.
func TestTraverseDedup(t *testing.T) {
	store := newEntityTestStore(t)

	// Both A and X link to B; B links to C. Traversing from C upstream for
	// two hops reaches B through both files but each edge appears once.
	if err := store.SaveRelations("/m/a.md", "A", []RelationRecord{
		{Type: "relates_to", Target: "B"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations A failed: %v", err)
	}
	if err := store.SaveRelations("/m/x.md", "X", []RelationRecord{
		{Type: "relates_to", Target: "B"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations X failed: %v", err)
	}
	if err := store.SaveRelations("/m/b.md", "B", []RelationRecord{
		{Type: "relates_to", Target: "C"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations B failed: %v", err)
	}

	edges, err := store.Traverse("C", 3, false)
	if err != nil {
		t.Fatalf("Traverse failed: %v", err)
	}
	if len(edges) != 3 {
		t.Fatalf("expected 3 unique edges, got %d: %+v", len(edges), edges)
	}
	seen := map[string]bool{}
	for _, e := range edges {
		key := e.FilePath + "|" + e.Type + "|" + e.Target + "|" + e.Context
		if seen[key] {
			t.Errorf("duplicate edge: %s", key)
		}
		seen[key] = true
	}
}

// TestFuzzyEntityResolve verifies ListIncoming falls back to entity-name
// fuzzy matching when the raw target has no exact alias.
func TestFuzzyEntityResolve(t *testing.T) {
	store := newEntityTestStore(t)

	if err := store.UpsertEntities([]EntityRecord{{Name: "design-spec"}}, 1000); err != nil {
		t.Fatalf("UpsertEntities failed: %v", err)
	}
	if err := store.SaveRelations("/m/a.md", "A", []RelationRecord{
		{Type: "implements", Target: "design-spec"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations failed: %v", err)
	}

	// Exact name resolves directly.
	rows, err := store.ListIncoming("design-spec")
	if err != nil || len(rows) != 1 {
		t.Fatalf("exact ListIncoming failed: %v (rows=%d)", err, len(rows))
	}

	// Prefix "design" resolves via the entities-table fuzzy fallback.
	rows, err = store.ListIncoming("design")
	if err != nil || len(rows) != 1 {
		t.Fatalf("fuzzy ListIncoming failed: %v (rows=%d)", err, len(rows))
	}
	if rows[0].Target != "design-spec" {
		t.Errorf("unexpected fuzzy result: %+v", rows[0])
	}
}
