package bytedp_memory

import "testing"

// TestSearchRelationTypeFilter verifies that RelationType filters search
// results to memories declaring an outgoing relation of the given type.
func TestSearchRelationTypeFilter(t *testing.T) {
	store := newTestStore(t)
	fileA := "/memdir/a.md"
	fileB := "/memdir/b.md"
	fileC := "/memdir/c.md"

	addTestRow(t, store, "coffee brewing methods guide", fileA, "a", "")
	addTestRow(t, store, "grinder maintenance checklist", fileB, "b", "")
	addTestRow(t, store, "coffee roasting notes", fileC, "c", "")

	if err := store.SaveRelations(fileA, "a", []RelationRecord{
		{Type: "implements", Target: "design-spec"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations A failed: %v", err)
	}
	if err := store.SaveRelations(fileC, "c", []RelationRecord{
		{Type: "relates_to", Target: "design-spec"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations C failed: %v", err)
	}

	// Type filter: only A declares an "implements" relation.
	opts := SearchOptions{
		Limit:        10,
		MinScore:     0,
		TextQuery:    "coffee",
		RelationType: "implements",
	}
	results, err := store.Search(nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for RelationType filter, got %d", len(results))
	}
	if results[0].Metadata.FilePath != fileA {
		t.Fatalf("expected result from %s, got %s", fileA, results[0].Metadata.FilePath)
	}

	// No type filter: A and C both mention coffee; B does not.
	opts.RelationType = ""
	results, err = store.Search(nil, opts)
	if err != nil {
		t.Fatalf("Search without type filter failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results without type filter, got %d", len(results))
	}
}

// TestSearchRelationTargetFilter verifies that RelationTarget filters
// search results to memories declaring a relation pointing at the target,
// resolving permalink and name aliases.
func TestSearchRelationTargetFilter(t *testing.T) {
	store := newTestStore(t)
	fileA := "/memdir/a.md"
	fileB := "/memdir/b.md"

	// Target memory indexed with permalink "design-spec" and name
	// "Design Spec" (name stored in entity_metadata, as the indexer does).
	addTestRow(t, store, "design specification document", "/memdir/target.md", "design-spec", "Design Spec")
	addTestRow(t, store, "coffee brewing methods guide", fileA, "a", "")
	addTestRow(t, store, "coffee roasting notes", fileB, "b", "")

	if err := store.SaveRelations(fileA, "a", []RelationRecord{
		{Type: "implements", Target: "design-spec"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations A failed: %v", err)
	}
	// B links by name; resolution must expand "design-spec" to include the
	// name alias so B matches too.
	if err := store.SaveRelations(fileB, "b", []RelationRecord{
		{Type: "relates_to", Target: "Design Spec"},
	}, 1000); err != nil {
		t.Fatalf("SaveRelations B failed: %v", err)
	}

	for _, target := range []string{"design-spec", "Design Spec"} {
		opts := SearchOptions{
			Limit:          10,
			MinScore:       0,
			TextQuery:      "coffee",
			RelationTarget: target,
		}
		results, err := store.Search(nil, opts)
		if err != nil {
			t.Fatalf("Search(RelationTarget=%q) failed: %v", target, err)
		}
		if len(results) != 2 {
			t.Fatalf("Search(RelationTarget=%q): expected 2 results, got %d", target, len(results))
		}
	}

	// Combined type+target: only A implements design-spec.
	opts := SearchOptions{
		Limit:          10,
		MinScore:       0,
		TextQuery:      "coffee",
		RelationType:   "implements",
		RelationTarget: "design-spec",
	}
	results, err := store.Search(nil, opts)
	if err != nil {
		t.Fatalf("combined filter Search failed: %v", err)
	}
	if len(results) != 1 || results[0].Metadata.FilePath != fileA {
		t.Fatalf("combined filter: expected 1 result from %s, got %+v", fileA, results)
	}

	// Unrelated target: nothing matches.
	opts = SearchOptions{
		Limit:          10,
		MinScore:       0,
		TextQuery:      "coffee",
		RelationTarget: "no-such-target",
	}
	results, err = store.Search(nil, opts)
	if err != nil {
		t.Fatalf("unrelated target Search failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for unrelated target, got %d", len(results))
	}
}
