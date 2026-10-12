package memory

import (
	"fmt"
	"os"
	"time"
)

// ==================== TTL Cleanup Methods ====================

// PurgeOldMemories deletes memories older than the specified number of days
// Returns the number of deleted memories
func (s *SQLiteStore) PurgeOldMemories(olderThanDays int) (int64, error) {
	if olderThanDays <= 0 {
		return 0, nil
	}

	cutoffTime := time.Now().AddDate(0, 0, -olderThanDays).Unix()

	// Check flags BEFORE acquiring lock
	vectorEnabled := s.isVectorEnabled(s.db)
	ftsEnabled := s.isFTSEnabled(s.db)

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Build delete condition
	deleteCondition := "WHERE created_at < ?"
	args := []interface{}{cutoffTime}

	// Preserve important memories if configured
	if s.ttlConfig.PreserveImportant {
		deleteCondition += " AND (importance IS NULL OR importance < 0.8)"
	}

	// Get IDs to delete first (for cleaning up vector and FTS tables)
	rows, err := tx.Query("SELECT id FROM memories "+deleteCondition, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to query old memories: %w", err)
	}

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	if len(ids) == 0 {
		return 0, nil
	}

	// Delete from memories table
	result, err := tx.Exec("DELETE FROM memories "+deleteCondition, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old memories: %w", err)
	}

	deletedCount, _ := result.RowsAffected()

	// Delete from vector table
	if vectorEnabled {
		for _, id := range ids {
			_, _ = tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE id = ?", s.vecTableName), id)
		}
	}

	// Delete from FTS table
	if ftsEnabled {
		for _, id := range ids {
			_, _ = tx.Exec("DELETE FROM memory_fts WHERE id = ?", id)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Run VACUUM to reclaim space (in background)
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		_, _ = s.db.Exec("VACUUM")
	}()

	return deletedCount, nil
}

// PurgeBySource deletes all memories from a specific source older than retention days
func (s *SQLiteStore) PurgeBySource(source MemorySource, olderThanDays int) (int64, error) {
	if olderThanDays <= 0 {
		return 0, nil
	}

	cutoffTime := time.Now().AddDate(0, 0, -olderThanDays).Unix()

	// Check flags BEFORE acquiring lock
	vectorEnabled := s.isVectorEnabled(s.db)
	ftsEnabled := s.isFTSEnabled(s.db)

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Get IDs to delete
	rows, err := tx.Query(
		"SELECT id FROM memories WHERE source = ? AND created_at < ?",
		string(source), cutoffTime)
	if err != nil {
		return 0, fmt.Errorf("failed to query old memories: %w", err)
	}

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	if len(ids) == 0 {
		return 0, nil
	}

	// Delete from memories table
	result, err := tx.Exec(
		"DELETE FROM memories WHERE source = ? AND created_at < ?",
		string(source), cutoffTime)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old memories: %w", err)
	}

	deletedCount, _ := result.RowsAffected()

	// Delete from vector table
	if vectorEnabled {
		for _, id := range ids {
			_, _ = tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE id = ?", s.vecTableName), id)
		}
	}

	// Delete from FTS table
	if ftsEnabled {
		for _, id := range ids {
			_, _ = tx.Exec("DELETE FROM memory_fts WHERE id = ?", id)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return deletedCount, nil
}

// startBackgroundCleanup starts the background cleanup goroutine
func (s *SQLiteStore) startBackgroundCleanup() {
	interval := s.ttlConfig.CleanupInterval
	if interval <= 0 {
		interval = 24 * time.Hour // Default: once per day
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Run cleanup
			deleted, err := s.PurgeOldMemories(s.ttlConfig.RetentionDays)
			if err != nil {
				fmt.Printf("Memory cleanup error: %v\n", err)
			} else if deleted > 0 {
				fmt.Printf("Memory cleanup: deleted %d old memories\n", deleted)
			}
		case <-s.stopCleanup:
			return
		}
	}
}

// StopCleanup stops the background cleanup goroutine
func (s *SQLiteStore) StopCleanup() {
	select {
	case <-s.stopCleanup:
		// Already closed
	default:
		close(s.stopCleanup)
	}
}

// GetStorageStats returns statistics about memory storage
func (s *SQLiteStore) GetStorageStats() (map[string]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]int64)

	// Total count
	var totalCount int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM memories").Scan(&totalCount)
	if err != nil {
		return nil, err
	}
	stats["total_count"] = totalCount

	// Count by source
	rows, err := s.db.Query("SELECT source, COUNT(*) FROM memories GROUP BY source")
	if err == nil {
		for rows.Next() {
			var source string
			var count int64
			if err := rows.Scan(&source, &count); err == nil {
				stats["source_"+source] = count
			}
		}
		rows.Close()
	}

	// Count by type
	rows, err = s.db.Query("SELECT type, COUNT(*) FROM memories GROUP BY type")
	if err == nil {
		for rows.Next() {
			var memType string
			var count int64
			if err := rows.Scan(&memType, &count); err == nil {
				stats["type_"+memType] = count
			}
		}
		rows.Close()
	}

	// Oldest memory
	var oldestTime int64
	err = s.db.QueryRow("SELECT MIN(created_at) FROM memories").Scan(&oldestTime)
	if err == nil && oldestTime > 0 {
		stats["oldest_timestamp"] = oldestTime
	}

	// Database file size
	var dbSize int64
	if info, err := os.Stat(s.dbPath); err == nil {
		dbSize = info.Size()
	}
	stats["db_size_bytes"] = dbSize

	return stats, nil
}
