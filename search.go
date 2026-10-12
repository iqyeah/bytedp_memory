package bytedp_memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MemoryManager manages memory storage and retrieval with LRU caching
type MemoryManager struct {
	store       Store
	provider    EmbeddingProvider
	mu          sync.RWMutex
	cache       *LRUCache
	initialized bool
}

// ManagerConfig configures the memory manager
type ManagerConfig struct {
	Store        Store
	Provider     EmbeddingProvider
	CacheMaxSize int
}

// DefaultManagerConfig returns default manager configuration
func DefaultManagerConfig(store Store, provider EmbeddingProvider) ManagerConfig {
	return ManagerConfig{
		Store:        store,
		Provider:     provider,
		CacheMaxSize: 1000,
	}
}

// NewMemoryManager creates a new memory manager with LRU cache
func NewMemoryManager(config ManagerConfig) (*MemoryManager, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if config.Provider == nil {
		return nil, fmt.Errorf("provider is required")
	}

	if config.CacheMaxSize == 0 {
		config.CacheMaxSize = 1000
	}

	cache := NewLRUCache(config.CacheMaxSize)

	return &MemoryManager{
		store:       config.Store,
		provider:    config.Provider,
		cache:       cache,
		initialized: true,
	}, nil
}

// AddMemory adds a new memory with automatic embedding generation
func (m *MemoryManager) AddMemory(ctx context.Context, text string, source MemorySource, memType MemoryType, metadata MemoryMetadata) (*VectorEmbedding, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Generate embedding OUTSIDE the lock to avoid blocking other operations
	embedding, err := m.provider.EmbedWithContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}

	ve := &VectorEmbedding{
		Vector:    embedding,
		Dimension: len(embedding),
		Text:      text,
		Source:    source,
		Type:      memType,
		Metadata:  metadata,
	}

	// Acquire lock only for the store operation
	m.mu.Lock()
	defer m.mu.Unlock()

	// Store the memory
	if err := m.store.Add(ve); err != nil {
		return nil, fmt.Errorf("failed to store memory: %w", err)
	}

	// Update cache
	m.updateCache(ve)

	return ve, nil
}

// AddMemoryBatch adds multiple memories with automatic embedding generation
func (m *MemoryManager) AddMemoryBatch(ctx context.Context, items []MemoryItem) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Prepare texts for embedding
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.Text
	}

	// Generate embeddings OUTSIDE the lock to avoid blocking other operations
	embeddings, err := m.provider.EmbedBatchWithContext(ctx, texts)
	if err != nil {
		return fmt.Errorf("failed to generate embeddings: %w", err)
	}

	// Create VectorEmbedding objects
	ves := make([]*VectorEmbedding, len(items))
	for i, item := range items {
		ves[i] = &VectorEmbedding{
			Vector:    embeddings[i],
			Dimension: len(embeddings[i]),
			Text:      item.Text,
			Source:    item.Source,
			Type:      item.Type,
			Metadata:  item.Metadata,
		}
	}

	// Acquire lock only for the store operation
	m.mu.Lock()
	defer m.mu.Unlock()

	// Store the memories
	if err := m.store.AddBatch(ves); err != nil {
		return fmt.Errorf("failed to store memories: %w", err)
	}

	// Update cache
	for _, ve := range ves {
		m.updateCache(ve)
	}

	return nil
}

// MemoryItem represents a memory to be added
type MemoryItem struct {
	Text     string
	Source   MemorySource
	Type     MemoryType
	Metadata MemoryMetadata
}

// Search searches for similar memories with advanced ranking options.
// When opts.TextQuery is set, bypasses vector embedding and uses FTS5 trigram.
func (m *MemoryManager) Search(ctx context.Context, query string, opts SearchOptions) ([]*SearchResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Text-only search path — skip embedding, go directly to FTS5.
	if opts.TextQuery != "" {
		m.mu.RLock()
		defer m.mu.RUnlock()
		results, err := m.store.Search(nil, opts)
		if err != nil {
			return nil, fmt.Errorf("text search failed: %w", err)
		}
		// Apply post-processing filters.
		results = m.postProcessResults(results, opts)
		// Add citations if requested
		if opts.IncludeCitations {
			for _, r := range results {
				r.Citation = formatCitation(&r.VectorEmbedding)
			}
		}
		return results, nil
	}

	// Vector path — generate query embedding.
	queryVec, err := m.provider.EmbedWithContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Acquire lock only for the search operation
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Perform search
	results, err := m.store.Search(queryVec, opts)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	results = m.postProcessResults(results, opts)

	// Add citations if requested
	if opts.IncludeCitations {
		for _, r := range results {
			r.Citation = formatCitation(&r.VectorEmbedding)
		}
	}

	return results, nil
}

// postProcessResults applies metadata filters, temporal decay, MMR, min
// score, and limit.
func (m *MemoryManager) postProcessResults(results []*SearchResult, opts SearchOptions) []*SearchResult {
	if len(opts.MetadataFilters) > 0 {
		results = filterByMetadata(results, opts.MetadataFilters)
	}
	if opts.TemporalDecay != nil && opts.TemporalDecay.Enabled {
		results = ApplyTemporalDecay(results, *opts.TemporalDecay)
	}
	if opts.MMR != nil && opts.MMR.Enabled {
		results = ApplyMMR(results, *opts.MMR)
	}
	if opts.MinScore > 0 {
		filtered := make([]*SearchResult, 0, len(results))
		for _, r := range results {
			if r.Score >= opts.MinScore {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return results
}

// filterByMetadata keeps only results whose metadata matches all given
// field-value pairs. Supported fields: file_path, session_key, description,
// permalink, and any EntityMetadata key.
func filterByMetadata(results []*SearchResult, filters map[string]string) []*SearchResult {
	filtered := make([]*SearchResult, 0, len(results))
	for _, r := range results {
		if metadataMatches(r.Metadata, filters) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// metadataMatches reports whether md satisfies every field-value pair.
func metadataMatches(md MemoryMetadata, filters map[string]string) bool {
	for field, want := range filters {
		var got string
		switch field {
		case "file_path":
			got = md.FilePath
		case "session_key":
			got = md.SessionKey
		case "description":
			got = md.Description
		case "permalink":
			got = md.Permalink
		default:
			got = md.EntityMetadata[field]
		}
		if got != want {
			return false
		}
	}
	return true
}

// Get retrieves a memory by ID
func (m *MemoryManager) Get(ctx context.Context, id string) (*VectorEmbedding, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.RLock()
	// Check LRU cache first (automatically updates position on hit)
	if ve, ok := m.cache.Get(id); ok {
		m.mu.RUnlock()
		return ve, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check in case another goroutine added it
	if ve, ok := m.cache.Get(id); ok {
		return ve, nil
	}

	// Fetch from store
	ve, err := m.store.Get(id)
	if err != nil {
		return nil, err
	}

	// Update cache (also moves to front)
	m.cache.Put(id, ve)

	return ve, nil
}

// Update updates an existing memory
func (m *MemoryManager) Update(ctx context.Context, ve *VectorEmbedding) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Re-generate embedding OUTSIDE the lock if text changed
	if len(ve.Vector) == 0 {
		embedding, err := m.provider.EmbedWithContext(ctx, ve.Text)
		if err != nil {
			return fmt.Errorf("failed to generate embedding: %w", err)
		}
		ve.Vector = embedding
		ve.Dimension = len(embedding)
	}

	// Acquire lock only for the update operation
	m.mu.Lock()
	defer m.mu.Unlock()

	// Update in store
	if err := m.store.Update(ve); err != nil {
		return fmt.Errorf("failed to update memory: %w", err)
	}

	// Update cache
	m.cache.Put(ve.ID, ve)

	return nil
}

// Delete removes a memory
func (m *MemoryManager) Delete(ctx context.Context, id string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Delete from store
	if err := m.store.Delete(id); err != nil {
		return fmt.Errorf("failed to delete memory: %w", err)
	}

	// Remove from cache
	m.cache.Delete(id)

	return nil
}

// List lists all memories with optional filtering
func (m *MemoryManager) List(ctx context.Context, filter func(*VectorEmbedding) bool) ([]*VectorEmbedding, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.store.List(filter)
}

// SearchBySource searches memories by source
func (m *MemoryManager) SearchBySource(ctx context.Context, source MemorySource) ([]*VectorEmbedding, error) {
	return m.List(ctx, func(ve *VectorEmbedding) bool {
		return ve.Source == source
	})
}

// SearchByType searches memories by type
func (m *MemoryManager) SearchByType(ctx context.Context, memType MemoryType) ([]*VectorEmbedding, error) {
	return m.List(ctx, func(ve *VectorEmbedding) bool {
		return ve.Type == memType
	})
}

// SearchByTag searches memories by tag
func (m *MemoryManager) SearchByTag(ctx context.Context, tag string) ([]*VectorEmbedding, error) {
	return m.List(ctx, func(ve *VectorEmbedding) bool {
		for _, t := range ve.Metadata.Tags {
			if strings.EqualFold(t, tag) {
				return true
			}
		}
		return false
	})
}

// SearchByText searches memories by text content (simple substring match).
// Prefer SearchByKeyword for CJK-aware FTS5 search with BM25 ranking.
func (m *MemoryManager) SearchByText(ctx context.Context, query string) ([]*VectorEmbedding, error) {
	queryLower := strings.ToLower(query)
	return m.List(ctx, func(ve *VectorEmbedding) bool {
		return strings.Contains(strings.ToLower(ve.Text), queryLower)
	})
}

// SearchByKeyword performs FTS5 trigram full-text search on memories.
// Supports CJK (Chinese/Japanese/Korean) natively. Falls back to LIKE
// for short queries (< 3 characters). Returns results sorted by BM25 relevance.
func (m *MemoryManager) SearchByKeyword(ctx context.Context, keyword string, limit int) ([]*SearchResult, error) {
	opts := DefaultSearchOptions()
	opts.TextQuery = keyword
	opts.Limit = limit
	opts.MinScore = 0 // no min threshold for keyword search
	return m.Search(ctx, "", opts)
}

// GetStats returns statistics about the memory store
func (m *MemoryManager) GetStats(ctx context.Context) (*MemoryStats, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	all, err := m.store.List(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}

	stats := &MemoryStats{
		TotalCount: len(all),
		CacheSize:  m.cache.Len(),
	}

	// Add storage stats if available
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		if storageStats, err := sqliteStore.GetStorageStats(); err == nil {
			stats.StorageStats = storageStats
		}
	}

	return stats, nil
}

// MemoryStats contains statistics about the memory store
type MemoryStats struct {
	TotalCount   int              `json:"total_count"`
	CacheSize    int              `json:"cache_size"`
	StorageStats map[string]int64 `json:"storage_stats,omitempty"`
}

// ClearCache clears the in-memory cache
func (m *MemoryManager) ClearCache() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cache.Clear()
}

// Close closes the memory manager
func (m *MemoryManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.initialized {
		return nil
	}

	m.initialized = false

	// Clear cache
	m.cache.Clear()

	// Close store
	return m.store.Close()
}

// updateCache updates the cache with a new memory
func (m *MemoryManager) updateCache(ve *VectorEmbedding) {
	// LRU cache handles eviction automatically
	m.cache.Put(ve.ID, ve)
}

// PurgeOldMemories deletes memories older than the specified number of days
// This is a pass-through to SQLiteStore.PurgeOldMemories
func (m *MemoryManager) PurgeOldMemories(olderThanDays int) (int64, error) {
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.PurgeOldMemories(olderThanDays)
	}
	return 0, fmt.Errorf("purge not supported for this store type")
}

// PurgeBySource deletes memories from a specific source older than retention days
func (m *MemoryManager) PurgeBySource(source MemorySource, olderThanDays int) (int64, error) {
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.PurgeBySource(source, olderThanDays)
	}
	return 0, fmt.Errorf("purge not supported for this store type")
}

// GetStorageStats returns detailed storage statistics
func (m *MemoryManager) GetStorageStats() (map[string]int64, error) {
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.GetStorageStats()
	}
	return nil, fmt.Errorf("storage stats not supported for this store type")
}

// ListIncomingRelations returns all relations pointing at the given memory
// identifier (a wiki-link target as written, a permalink, or a name).
func (m *MemoryManager) ListIncomingRelations(ctx context.Context, target string) ([]RelationRecord, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.ListIncoming(target)
	}
	return nil, fmt.Errorf("relation queries not supported for this store type")
}

// ListOutgoingRelations returns all relations declared by the given file.
func (m *MemoryManager) ListOutgoingRelations(ctx context.Context, filePath string) ([]RelationRecord, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.ListOutgoing(filePath)
	}
	return nil, fmt.Errorf("relation queries not supported for this store type")
}

// UpsertEntities upserts entity records (see SQLiteStore.UpsertEntities).
// mtimeMs is applied to every record.
func (m *MemoryManager) UpsertEntities(ctx context.Context, entities []EntityRecord, mtimeMs int64) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.UpsertEntities(entities, mtimeMs)
	}
	return fmt.Errorf("entity operations not supported for this store type")
}

// ListEntities returns entities ordered by modification time descending.
func (m *MemoryManager) ListEntities(ctx context.Context, limit int) ([]EntityRecord, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.ListEntities(limit)
	}
	return nil, fmt.Errorf("entity operations not supported for this store type")
}

// GetEntity returns one entity by exact name (nil when absent).
func (m *MemoryManager) GetEntity(ctx context.Context, name string) (*EntityRecord, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.GetEntity(name)
	}
	return nil, fmt.Errorf("entity operations not supported for this store type")
}

// DeleteEntity removes an entity by name.
func (m *MemoryManager) DeleteEntity(ctx context.Context, name string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.DeleteEntity(name)
	}
	return fmt.Errorf("entity operations not supported for this store type")
}

// ListEntityRelations returns the entity's relation edges: incoming ones
// pointing at it (incoming=true) or outgoing ones declared by its memory
// file (incoming=false).
func (m *MemoryManager) ListEntityRelations(ctx context.Context, name string, incoming bool) ([]RelationRecord, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.ListEntityRelations(name, incoming)
	}
	return nil, fmt.Errorf("entity operations not supported for this store type")
}

// TraverseRelations expands the relation graph from a start entity name up
// to hops levels, returning the deduplicated edge set (bidirectional walk).
func (m *MemoryManager) TraverseRelations(ctx context.Context, start string, hops int) ([]RelationRecord, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		return sqliteStore.Traverse(start, hops, false)
	}
	return nil, fmt.Errorf("entity operations not supported for this store type")
}
