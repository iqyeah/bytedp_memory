package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// mockVecProvider returns deterministic pseudo-random vectors so KNN
// ordering is stable across runs without a real embedding backend.
type mockVecProvider struct{ dim int }

func (m *mockVecProvider) Embed(text string) ([]float32, error) {
	return deterministicTestVec(text, m.dim), nil
}

func (m *mockVecProvider) EmbedWithContext(ctx context.Context, text string) ([]float32, error) {
	return deterministicTestVec(text, m.dim), nil
}

func (m *mockVecProvider) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = deterministicTestVec(t, m.dim)
	}
	return out, nil
}

func (m *mockVecProvider) EmbedBatchWithContext(ctx context.Context, texts []string) ([][]float32, error) {
	return m.EmbedBatch(texts)
}

func (m *mockVecProvider) Dimension() int      { return m.dim }
func (m *mockVecProvider) MaxBatchSize() int   { return 16 }
func (m *mockVecProvider) VecTableName() string { return "vec_test_table" }

// deterministicTestVec derives a stable dim-length vector from seed via
// splitmix64 mixing (values in [-1, 1)).
func deterministicTestVec(seed string, dim int) []float32 {
	var h uint64 = 14695981039346656037 // FNV-1a offset basis
	for i := 0; i < len(seed); i++ {
		h ^= uint64(seed[i])
		h *= 1099511628211
	}
	v := make([]float32, dim)
	for i := range v {
		h ^= h >> 33
		h *= 0xff51afd7ed558ccd
		h ^= h >> 33
		v[i] = float32(h%200)/100 - 1
	}
	return v
}

// newVectorTestStore opens a store with vector search enabled and a
// deterministic mock provider (no real embedding API call).
func newVectorTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	cfg := DefaultStoreConfig(filepath.Join(t.TempDir(), "memory.db"), &mockVecProvider{dim: 8})
	cfg.EnableVectorSearch = true
	cfg.EnableFTS = true
	store, err := NewSQLiteStore(cfg)
	if err != nil {
		t.Fatalf("failed to create vector store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// l2Dist computes squared L2 distance between two vectors.
func l2Dist(a, b []float32) float64 {
	var sum float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return sum
}

// TestVectorSearchWorks verifies the sqlite-vec path end to end: vec0
// virtual table creation, vector insert, KNN MATCH query, and distance
// ordering. It must return non-empty results — before the modernc.org/sqlite/vec
// switch, vector search was silently unavailable (load_extension failed on
// the pure-Go driver).
func TestVectorSearchWorks(t *testing.T) {
	store := newVectorTestStore(t)

	seeds := []string{"alpha", "beta", "gamma"}
	for _, seed := range seeds {
		ve := &VectorEmbedding{
			ID:        "id-" + seed,
			Text:      "memory about " + seed,
			Source:    MemorySourceLongTerm,
			Type:      MemoryTypeUser,
			Vector:    deterministicTestVec(seed, 8),
			CreatedAt: time.Now(),
		}
		if err := store.Add(ve); err != nil {
			t.Fatalf("Add(%s) failed: %v", seed, err)
		}
	}

	query := deterministicTestVec("beta-ish", 8)
	opts := DefaultSearchOptions()
	opts.Hybrid = false // exercise the pure vector channel only
	opts.MinScore = 0
	opts.Limit = 10

	results, err := store.Search(query, opts)
	if err != nil {
		t.Fatalf("vector Search failed: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 vector hits, got %d: %+v", len(results), results)
	}

	// The nearest seed must rank first (distance is monotonically ordered).
	want := seeds[0]
	best := 1e18
	for _, seed := range seeds {
		if d := l2Dist(query, deterministicTestVec(seed, 8)); d < best {
			best = d
			want = seed
		}
	}
	if results[0].ID != "id-"+want {
		t.Errorf("top hit = %s, want %s (dist=%.4f); full order: %+v",
			results[0].ID, "id-"+want, best, results)
	}
}

// TestVectorSearchUpdateDelete verifies vec-table maintenance on delete
// (a deleted row must not come back from KNN queries).
func TestVectorSearchUpdateDelete(t *testing.T) {
	store := newVectorTestStore(t)

	ve := &VectorEmbedding{
		ID:        "id-alpha",
		Text:      "memory about alpha",
		Source:    MemorySourceLongTerm,
		Type:      MemoryTypeUser,
		Vector:    deterministicTestVec("alpha", 8),
		CreatedAt: time.Now(),
	}
	if err := store.Add(ve); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if err := store.Add(&VectorEmbedding{
		ID:        "id-zeta",
		Text:      "memory about zeta",
		Source:    MemorySourceLongTerm,
		Type:      MemoryTypeUser,
		Vector:    deterministicTestVec("zeta", 8),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Add zeta failed: %v", err)
	}

	opts := DefaultSearchOptions()
	opts.Hybrid = false
	opts.MinScore = 0
	opts.Limit = 10

	results, _ := store.Search(deterministicTestVec("alpha", 8), opts)
	if len(results) != 2 {
		t.Fatalf("expected 2 hits before delete, got %d", len(results))
	}

	if err := store.Delete("id-alpha"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	results, err := store.Search(deterministicTestVec("alpha", 8), opts)
	if err != nil {
		t.Fatalf("post-delete Search failed: %v", err)
	}
	if len(results) != 1 || results[0].ID != "id-zeta" {
		t.Errorf("expected only id-zeta after delete, got %+v", results)
	}
}
