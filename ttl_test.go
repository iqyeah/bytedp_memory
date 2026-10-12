package bytedp_memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTTLConfig(t *testing.T) {
	// Create temp database
	tmpDir, err := os.MkdirTemp("", "memory_ttl_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create store with TTL config
	config := StoreConfig{
		DBPath: dbPath,
		TTLConfig: TTLConfig{
			Enabled:           true,
			RetentionDays:     30,
			CleanupInterval:   1 * time.Hour,
			PreserveImportant: true,
		},
	}

	store, err := NewSQLiteStore(config)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Verify TTL config is set
	if !store.ttlConfig.Enabled {
		t.Error("TTL should be enabled")
	}
	if store.ttlConfig.RetentionDays != 30 {
		t.Errorf("Expected retention days 30, got %d", store.ttlConfig.RetentionDays)
	}
}

func TestPurgeOldMemories(t *testing.T) {
	// Create temp database
	tmpDir, err := os.MkdirTemp("", "memory_purge_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create store WITHOUT auto-cleanup (we'll test manual purge)
	config := StoreConfig{
		DBPath: dbPath,
		TTLConfig: TTLConfig{
			Enabled:       false, // Disable auto-cleanup for testing
			RetentionDays: 30,
		},
	}

	store, err := NewSQLiteStore(config)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Add old memory (40 days ago)
	oldTime := time.Now().AddDate(0, 0, -40)
	oldMemory := &VectorEmbedding{
		ID:        "old-memory-1",
		Text:      "This is an old memory",
		Source:    MemorySourceSession,
		Type:      MemoryTypeConversation,
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
	}
	if err := store.Add(oldMemory); err != nil {
		t.Fatalf("failed to add old memory: %v", err)
	}

	// Add new memory (today)
	newMemory := &VectorEmbedding{
		ID:        "new-memory-1",
		Text:      "This is a new memory",
		Source:    MemorySourceSession,
		Type:      MemoryTypeConversation,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.Add(newMemory); err != nil {
		t.Fatalf("failed to add new memory: %v", err)
	}

	// Add important old memory (should be preserved if PreserveImportant is true)
	importantOldMemory := &VectorEmbedding{
		ID:        "important-old-memory",
		Text:      "This is an important old memory",
		Source:    MemorySourceLongTerm,
		Type:      MemoryTypeFact,
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
	}
	importantOldMemory.Metadata.Importance = 0.9
	if err := store.Add(importantOldMemory); err != nil {
		t.Fatalf("failed to add important old memory: %v", err)
	}

	// Verify we have 3 memories
	stats, err := store.GetStorageStats()
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats["total_count"] != 3 {
		t.Errorf("Expected 3 memories, got %d", stats["total_count"])
	}

	// Purge memories older than 30 days (with PreserveImportant=true)
	store.ttlConfig.PreserveImportant = true
	deleted, err := store.PurgeOldMemories(30)
	if err != nil {
		t.Fatalf("failed to purge: %v", err)
	}

	if deleted != 1 {
		t.Errorf("Expected 1 deleted memory, got %d", deleted)
	}

	// Verify we have 2 memories left (new + important old)
	stats, err = store.GetStorageStats()
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats["total_count"] != 2 {
		t.Errorf("Expected 2 memories after purge, got %d", stats["total_count"])
	}

	// Verify old memory is deleted
	_, err = store.Get("old-memory-1")
	if err == nil {
		t.Error("Old memory should be deleted")
	}

	// Verify new memory still exists
	_, err = store.Get("new-memory-1")
	if err != nil {
		t.Error("New memory should still exist")
	}

	// Verify important old memory still exists
	_, err = store.Get("important-old-memory")
	if err != nil {
		t.Error("Important old memory should be preserved")
	}

	// Test purge without PreserveImportant
	store.ttlConfig.PreserveImportant = false
	deleted, err = store.PurgeOldMemories(30)
	if err != nil {
		t.Fatalf("failed to purge: %v", err)
	}
	if deleted != 1 {
		t.Errorf("Expected 1 deleted (important old), got %d", deleted)
	}
}

func TestPurgeBySource(t *testing.T) {
	// Create temp database
	tmpDir, err := os.MkdirTemp("", "memory_purge_source_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	config := StoreConfig{
		DBPath:    dbPath,
		TTLConfig: TTLConfig{Enabled: false},
	}

	store, err := NewSQLiteStore(config)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Add old session memory
	oldTime := time.Now().AddDate(0, 0, -40)
	oldSession := &VectorEmbedding{
		ID:        "old-session",
		Text:      "Old session memory",
		Source:    MemorySourceSession,
		Type:      MemoryTypeConversation,
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
	}
	if err := store.Add(oldSession); err != nil {
		t.Fatalf("failed to add: %v", err)
	}

	// Add old longterm memory (should not be affected)
	oldLongterm := &VectorEmbedding{
		ID:        "old-longterm",
		Text:      "Old longterm memory",
		Source:    MemorySourceLongTerm,
		Type:      MemoryTypeFact,
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
	}
	if err := store.Add(oldLongterm); err != nil {
		t.Fatalf("failed to add: %v", err)
	}

	// Add new session memory
	newSession := &VectorEmbedding{
		ID:        "new-session",
		Text:      "New session memory",
		Source:    MemorySourceSession,
		Type:      MemoryTypeConversation,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.Add(newSession); err != nil {
		t.Fatalf("failed to add: %v", err)
	}

	// Purge only session memories older than 30 days
	deleted, err := store.PurgeBySource(MemorySourceSession, 30)
	if err != nil {
		t.Fatalf("failed to purge: %v", err)
	}

	if deleted != 1 {
		t.Errorf("Expected 1 deleted session memory, got %d", deleted)
	}

	// Verify counts
	stats, _ := store.GetStorageStats()
	if stats["total_count"] != 2 {
		t.Errorf("Expected 2 memories left, got %d", stats["total_count"])
	}

	// Verify old session is deleted
	_, err = store.Get("old-session")
	if err == nil {
		t.Error("Old session should be deleted")
	}

	// Verify old longterm still exists
	_, err = store.Get("old-longterm")
	if err != nil {
		t.Error("Old longterm should still exist")
	}
}

func TestGetStorageStats(t *testing.T) {
	// Create temp database
	tmpDir, err := os.MkdirTemp("", "memory_stats_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	config := StoreConfig{
		DBPath:    dbPath,
		TTLConfig: TTLConfig{Enabled: false},
	}

	store, err := NewSQLiteStore(config)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Add some memories
	for i := 0; i < 5; i++ {
		mem := &VectorEmbedding{
			ID:        string(rune('a' + i)),
			Text:      "Test memory",
			Source:    MemorySourceSession,
			Type:      MemoryTypeConversation,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := store.Add(mem); err != nil {
			t.Fatalf("failed to add: %v", err)
		}
	}

	stats, err := store.GetStorageStats()
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}

	if stats["total_count"] != 5 {
		t.Errorf("Expected total_count 5, got %d", stats["total_count"])
	}

	if stats["db_size_bytes"] <= 0 {
		t.Error("db_size_bytes should be positive")
	}

	if stats["source_session"] != 5 {
		t.Errorf("Expected source_session 5, got %d", stats["source_session"])
	}
}
