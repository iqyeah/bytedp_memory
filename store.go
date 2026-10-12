package bytedp_memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec" // sqlite-vec: registers the vec0 module on every connection (CGo-free)
)

// SQLExecutor is a common interface for sql.DB and sql.Tx
// This allows methods to work with both transaction and non-transaction contexts

type SQLExecutor interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryRow(query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	Exec(query string, args ...any) (sql.Result, error)
}

// SQLiteStore implements the Store interface using SQLite
type SQLiteStore struct {
	db           *sql.DB
	dbPath       string
	provider     EmbeddingProvider
	vecTableName string // model-specific vector table name
	mu           TimeoutRWMutex
	initialized  bool
	ttlConfig    TTLConfig
	stopCleanup  chan struct{}
}

// StoreConfig configures the SQLite memory store
type StoreConfig struct {
	// DBPath is the path to the SQLite database file
	DBPath string
	// Provider is the embedding provider to use
	Provider EmbeddingProvider
	// EnableVectorSearch enables vector similarity search (requires sqlite-vec)
	EnableVectorSearch bool
	// EnableFTS enables full-text search
	EnableFTS bool
	// VectorExtensionPath is the path to the sqlite-vec extension.
	// Deprecated: the vec0 module is now bundled via modernc.org/sqlite/vec
	// (auto-registered on every connection); no external extension file is needed.
	VectorExtensionPath string
	// TTLConfig configures automatic cleanup of old memories
	TTLConfig TTLConfig
}

// TTLConfig configures time-based memory cleanup
type TTLConfig struct {
	// Enabled enables automatic TTL cleanup
	Enabled bool
	// RetentionDays is the number of days to keep memories (0 = no limit)
	RetentionDays int
	// CleanupInterval is how often to run cleanup (default: 24 hours)
	CleanupInterval time.Duration
	// PreserveImportant if true, never delete memories with importance >= 0.8
	PreserveImportant bool
}

// DefaultStoreConfig returns default store configuration
func DefaultStoreConfig(dbPath string, provider EmbeddingProvider) StoreConfig {
	return StoreConfig{
		DBPath:              dbPath,
		Provider:            provider,
		EnableVectorSearch:  true,
		EnableFTS:           true,
		VectorExtensionPath: "",
	}
}

// NewSQLiteStore creates a new SQLite-based memory store
func NewSQLiteStore(config StoreConfig) (*SQLiteStore, error) {
	if config.DBPath == "" {
		return nil, fmt.Errorf("database path is required")
	}

	// Ensure directory exists
	dir := filepath.Dir(config.DBPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database connection
	db, err := sql.Open("sqlite", config.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings for SQLite
	// SQLite works best with single connection due to file locking
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	// Set connection lifetime to prevent stale connections
	db.SetConnMaxLifetime(30 * time.Minute)
	// Set idle connection timeout
	db.SetConnMaxIdleTime(5 * time.Minute)

	vecTableName := "memory_vec"
	if config.Provider != nil {
		vecTableName = config.Provider.VecTableName()
	}

	store := &SQLiteStore{
		db:           db,
		dbPath:       config.DBPath,
		provider:     config.Provider,
		vecTableName: vecTableName,
		mu:           *NewTimeoutRWMutex(),
		ttlConfig:    config.TTLConfig,
		stopCleanup:  make(chan struct{}),
	}

	// Initialize schema
	if err := store.initSchema(config); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	store.initialized = true

	// Start background cleanup if TTL is enabled
	if config.TTLConfig.Enabled && config.TTLConfig.RetentionDays > 0 {
		go store.startBackgroundCleanup()
	}

	return store, nil
}

// Add adds a memory to the store
func (s *SQLiteStore) Add(embedding *VectorEmbedding) error {
	if embedding.ID == "" {
		embedding.ID = uuid.New().String()
	}
	if embedding.CreatedAt.IsZero() {
		embedding.CreatedAt = time.Now()
	}
	embedding.UpdatedAt = time.Now()

	// Check flags BEFORE acquiring lock and starting transaction
	// to avoid deadlock with single connection
	vectorEnabled := s.isVectorEnabled(s.db)
	ftsEnabled := s.isFTSEnabled(s.db)

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize tags
	var tagsJSON string
	if len(embedding.Metadata.Tags) > 0 {
		tagsBytes, err := json.Marshal(embedding.Metadata.Tags)
		if err == nil {
			tagsJSON = string(tagsBytes)
		}
	}

	// Serialize embedding
	var embeddingJSON string
	if len(embedding.Vector) > 0 {
		embBytes, err := json.Marshal(embedding.Vector)
		if err != nil {
			return fmt.Errorf("failed to marshal embedding: %w", err)
		}
		embeddingJSON = string(embBytes)
	}

	// Serialize entity metadata
	var entityMetaJSON string
	if len(embedding.Metadata.EntityMetadata) > 0 {
		metaBytes, err := json.Marshal(embedding.Metadata.EntityMetadata)
		if err == nil {
			entityMetaJSON = string(metaBytes)
		}
	}

	// Bind NULL (not "") for missing observation keys so the unique index
	// treats non-observation rows as distinct.
	var observationKey interface{}
	if embedding.Metadata.ObservationKey != "" {
		observationKey = embedding.Metadata.ObservationKey
	}

	// Insert into memories table. Observation rows (non-empty ObservationKey)
	// are upserted idempotently via the unique index.
	res, err := tx.Exec(`
		INSERT INTO memories (
			id, text, source, type, embedding, dimension,
			created_at, updated_at, file_path, line_number,
			session_key, tags, importance, access_count, last_accessed,
			observation_key, entity_metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(observation_key) DO NOTHING
	`, embedding.ID, embedding.Text, embedding.Source, embedding.Type,
		embeddingJSON, len(embedding.Vector), embedding.CreatedAt.Unix(),
		embedding.UpdatedAt.Unix(), embedding.Metadata.FilePath,
		embedding.Metadata.LineNumber, embedding.Metadata.SessionKey,
		tagsJSON, embedding.Metadata.Importance, embedding.Metadata.AccessCount,
		embedding.Metadata.LastAccessed.Unix(),
		observationKey, entityMetaJSON)

	if err != nil {
		return fmt.Errorf("failed to insert memory: %w", err)
	}

	if affected, _ := res.RowsAffected(); affected == 0 {
		// Observation key already indexed — idempotent skip.
		return nil
	}

	// Insert into vector table if enabled (use pre-checked flag)
	if vectorEnabled && len(embedding.Vector) > 0 {
		if err := s.insertVector(tx, embedding.ID, embedding.Vector); err != nil {
			return fmt.Errorf("failed to insert vector: %w", err)
		}
	}

	// Insert into FTS if enabled (use pre-checked flag)
	if ftsEnabled {
		if err := s.insertFTS(tx, embedding); err != nil {
			return fmt.Errorf("failed to insert FTS: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// AddBatch adds multiple memories in one transaction
func (s *SQLiteStore) AddBatch(embeddings []*VectorEmbedding) error {
	// Check flags BEFORE acquiring lock and starting transaction
	// to avoid deadlock with single connection
	vectorEnabled := s.isVectorEnabled(s.db)
	ftsEnabled := s.isFTSEnabled(s.db)

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT INTO memories (
			id, text, source, type, embedding, dimension,
			created_at, updated_at, file_path, line_number,
			session_key, tags, importance, access_count, last_accessed,
			observation_key, entity_metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(observation_key) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, emb := range embeddings {
		if emb.ID == "" {
			emb.ID = uuid.New().String()
		}
		if emb.CreatedAt.IsZero() {
			emb.CreatedAt = time.Now()
		}
		emb.UpdatedAt = time.Now()

		var tagsJSON string
		if len(emb.Metadata.Tags) > 0 {
			tagsBytes, _ := json.Marshal(emb.Metadata.Tags)
			tagsJSON = string(tagsBytes)
		}

		var embeddingJSON string
		if len(emb.Vector) > 0 {
			embBytes, _ := json.Marshal(emb.Vector)
			embeddingJSON = string(embBytes)
		}

		var entityMetaJSON string
		if len(emb.Metadata.EntityMetadata) > 0 {
			metaBytes, _ := json.Marshal(emb.Metadata.EntityMetadata)
			entityMetaJSON = string(metaBytes)
		}

		// Bind NULL (not "") for missing observation keys so the unique
		// index treats non-observation rows as distinct.
		var observationKey interface{}
		if emb.Metadata.ObservationKey != "" {
			observationKey = emb.Metadata.ObservationKey
		}

		res, err := stmt.Exec(
			emb.ID, emb.Text, emb.Source, emb.Type,
			embeddingJSON, len(emb.Vector), emb.CreatedAt.Unix(),
			emb.UpdatedAt.Unix(), emb.Metadata.FilePath,
			emb.Metadata.LineNumber, emb.Metadata.SessionKey,
			tagsJSON, emb.Metadata.Importance, emb.Metadata.AccessCount,
			emb.Metadata.LastAccessed.Unix(),
			observationKey, entityMetaJSON,
		)
		if err != nil {
			return fmt.Errorf("failed to insert memory %s: %w", emb.ID, err)
		}

		if affected, _ := res.RowsAffected(); affected == 0 {
			// Observation key already indexed — idempotent skip.
			continue
		}

		// Use pre-checked flags to avoid deadlock
		if vectorEnabled && len(emb.Vector) > 0 {
			if err := s.insertVector(tx, emb.ID, emb.Vector); err != nil {
				return fmt.Errorf("failed to insert vector for %s: %w", emb.ID, err)
			}
		}

		// Use pre-checked flags to avoid deadlock
		if ftsEnabled {
			if err := s.insertFTS(tx, emb); err != nil {
				return fmt.Errorf("failed to insert FTS for %s: %w", emb.ID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// insertVector inserts a vector into the vec0 table
func (s *SQLiteStore) insertVector(tx *sql.Tx, id string, vector []float32) error {
	// Convert float32 slice to comma-separated string
	vectorStr := float32SliceToString(vector)

	// vec0 (sqlite-vec) does not implement UPSERT; emulate overwrite with
	// delete + insert.
	if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.vecTableName), id); err != nil {
		return err
	}

	_, err := tx.Exec(fmt.Sprintf(`
		INSERT INTO %s (id, embedding)
		VALUES (?, ?)
	`, s.vecTableName), id, vectorStr)

	return err
}

// insertFTS inserts text into the FTS table
// Note: FTS5 virtual tables do not support UPSERT, so we use DELETE + INSERT
func (s *SQLiteStore) insertFTS(tx *sql.Tx, embedding *VectorEmbedding) error {
	// FTS5 doesn't support UPSERT, so we delete first then insert
	_, _ = tx.Exec(`DELETE FROM memory_fts WHERE id = ?`, embedding.ID)

	_, err := tx.Exec(`
		INSERT INTO memory_fts (text, id, source, type)
		VALUES (?, ?, ?, ?)
	`, embedding.Text, embedding.ID, embedding.Source, embedding.Type)

	return err
}

// Search performs vector similarity search and/or FTS5 text search.
// Use TextQuery for keyword search (CJK-aware with trigram tokenizer),
// or pass a vector for semantic search. Both can be combined via Hybrid mode.
func (s *SQLiteStore) Search(query []float32, opts SearchOptions) (results []*SearchResult, err error) {
	if opts.Trace != nil {
		start := time.Now()
		defer func() {
			opts.Trace.Duration = time.Since(start)
			opts.Trace.Hits = len(results)
		}()
	}

	// Text-only search: skip embedding, go straight to FTS5.
	if opts.TextQuery != "" {
		ftsEnabled := s.isFTSEnabled(s.db)
		if ftsEnabled {
			s.mu.RLock()
			defer s.mu.RUnlock()
			if opts.Trace != nil {
				opts.Trace.Mode = "fts"
			}
			return s.searchFTSByText(opts.TextQuery, opts)
		}
		// FTS not available — fallback to basic substring (via caller).
		return nil, nil
	}

	// Vector requires a non-empty query.
	if len(query) == 0 {
		return nil, fmt.Errorf("query vector is empty")
	}

	// Check flags BEFORE acquiring lock to avoid potential deadlock
	vectorEnabled := s.isVectorEnabled(s.db)
	ftsEnabled := s.isFTSEnabled(s.db)

	s.mu.RLock()
	defer s.mu.RUnlock()

	// If vector search is enabled, use it (use pre-checked flag)
	if vectorEnabled {
		if opts.Trace != nil {
			opts.Trace.Mode = "vector"
		}
		results, err := s.searchVector(query, opts)
		if err != nil {
			return nil, fmt.Errorf("vector search failed: %w", err)
		}

		// If hybrid search is enabled and FTS is available, combine results
		if opts.Hybrid && ftsEnabled {
			ftsResults, err := s.searchFTS(query, opts)
			if err == nil && len(ftsResults) > 0 {
				if opts.Trace != nil {
					opts.Trace.Mode = "hybrid"
				}
				return s.mergeHybridResults(results, ftsResults, opts), nil
			}
		}

		return results, nil
	}

	// Fallback to basic text search (use pre-checked flag)
	if ftsEnabled {
		return s.searchFTS(query, opts)
	}

	// No search available, return empty
	return results, nil
}

// Get retrieves a memory by ID
func (s *SQLiteStore) Get(id string) (*VectorEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ve VectorEmbedding
	var embeddingJSON sql.NullString
	var tagsJSON sql.NullString
	var lastAccessed sql.NullInt64
	var filePath sql.NullString
	var sessionKey sql.NullString
	var lineNumber sql.NullInt64
	var importance sql.NullFloat64
	var accessCount sql.NullInt64
	var observationKey sql.NullString
	var entityMeta sql.NullString
	var createdAtInt, updatedAtInt int64

	err := s.db.QueryRow(`
		SELECT
			id, text, source, type, embedding, created_at, updated_at,
			file_path, line_number, session_key, tags, importance,
			access_count, last_accessed, observation_key, entity_metadata
		FROM memories
		WHERE id = ?
	`, id).Scan(
		&ve.ID,
		&ve.Text,
		&ve.Source,
		&ve.Type,
		&embeddingJSON,
		&createdAtInt,
		&updatedAtInt,
		&filePath,
		&lineNumber,
		&sessionKey,
		&tagsJSON,
		&importance,
		&accessCount,
		&lastAccessed,
		&observationKey,
		&entityMeta,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("memory not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}

	// Convert timestamps
	ve.CreatedAt = time.Unix(createdAtInt, 0)
	ve.UpdatedAt = time.Unix(updatedAtInt, 0)

	// Unmarshal embedding
	if embeddingJSON.Valid {
		_ = json.Unmarshal([]byte(embeddingJSON.String), &ve.Vector)
		ve.Dimension = len(ve.Vector)
	}

	// Populate metadata fields
	ve.Metadata.FilePath = filePath.String
	ve.Metadata.LineNumber = int(lineNumber.Int64)
	ve.Metadata.SessionKey = sessionKey.String
	ve.Metadata.Importance = importance.Float64
	ve.Metadata.AccessCount = int(accessCount.Int64)
	ve.Metadata.ObservationKey = observationKey.String
	if entityMeta.Valid {
		_ = json.Unmarshal([]byte(entityMeta.String), &ve.Metadata.EntityMetadata)
	}

	// Unmarshal tags
	if tagsJSON.Valid {
		_ = json.Unmarshal([]byte(tagsJSON.String), &ve.Metadata.Tags)
	}

	// Handle last accessed
	if lastAccessed.Valid {
		ve.Metadata.LastAccessed = time.Unix(lastAccessed.Int64, 0)
	}

	// Update access count
	go s.updateAccessCount(id)

	return &ve, nil
}

// Delete removes a memory by ID
func (s *SQLiteStore) Delete(id string) error {
	// Check flags BEFORE acquiring lock and starting transaction
	// to avoid deadlock with single connection
	vectorEnabled := s.isVectorEnabled(s.db)
	ftsEnabled := s.isFTSEnabled(s.db)

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete from memories table
	if _, err := tx.Exec(`DELETE FROM memories WHERE id = ?`, id); err != nil {
		return fmt.Errorf("failed to delete memory: %w", err)
	}

	// Delete from vector table (use pre-checked flag)
	if vectorEnabled {
		if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.vecTableName), id); err != nil {
			return fmt.Errorf("failed to delete vector: %w", err)
		}
	}

	// Delete from FTS table (use pre-checked flag)
	if ftsEnabled {
		if _, err := tx.Exec(`DELETE FROM memory_fts WHERE id = ?`, id); err != nil {
			return fmt.Errorf("failed to delete FTS: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Update updates an existing memory
func (s *SQLiteStore) Update(embedding *VectorEmbedding) error {
	embedding.UpdatedAt = time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	var tagsJSON string
	if len(embedding.Metadata.Tags) > 0 {
		tagsBytes, _ := json.Marshal(embedding.Metadata.Tags)
		tagsJSON = string(tagsBytes)
	}

	var embeddingJSON string
	if len(embedding.Vector) > 0 {
		embBytes, _ := json.Marshal(embedding.Vector)
		embeddingJSON = string(embBytes)
	}

	var entityMetaJSON string
	if len(embedding.Metadata.EntityMetadata) > 0 {
		metaBytes, _ := json.Marshal(embedding.Metadata.EntityMetadata)
		entityMetaJSON = string(metaBytes)
	}

	// Bind NULL (not "") for missing observation keys so the unique index
	// treats non-observation rows as distinct.
	var observationKey interface{}
	if embedding.Metadata.ObservationKey != "" {
		observationKey = embedding.Metadata.ObservationKey
	}

	_, err := s.db.Exec(`
		UPDATE memories SET
			text = ?,
			source = ?,
			type = ?,
			embedding = ?,
			dimension = ?,
			updated_at = ?,
			file_path = ?,
			line_number = ?,
			session_key = ?,
			tags = ?,
			importance = ?,
			access_count = ?,
			last_accessed = ?,
			observation_key = ?,
			entity_metadata = ?
		WHERE id = ?
	`, embedding.Text, embedding.Source, embedding.Type,
		embeddingJSON, len(embedding.Vector), embedding.UpdatedAt.Unix(),
		embedding.Metadata.FilePath, embedding.Metadata.LineNumber,
		embedding.Metadata.SessionKey, tagsJSON, embedding.Metadata.Importance,
		embedding.Metadata.AccessCount, embedding.Metadata.LastAccessed.Unix(),
		observationKey, entityMetaJSON,
		embedding.ID)

	if err != nil {
		return fmt.Errorf("failed to update memory: %w", err)
	}

	return nil
}

// List lists all memories with optional filtering
func (s *SQLiteStore) List(filter func(*VectorEmbedding) bool) ([]*VectorEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT
			id, text, source, type, embedding, created_at, updated_at,
			file_path, line_number, session_key, tags, importance,
			access_count, last_accessed, observation_key, entity_metadata
		FROM memories
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}
	defer rows.Close()

	var results []*VectorEmbedding
	for rows.Next() {
		var ve VectorEmbedding
		var embeddingJSON sql.NullString
		var tagsJSON sql.NullString
		var lastAccessed sql.NullInt64
		var filePath sql.NullString
		var sessionKey sql.NullString
		var lineNumber sql.NullInt64
		var importance sql.NullFloat64
		var accessCount sql.NullInt64
		var observationKey sql.NullString
		var entityMeta sql.NullString
		var createdAtInt, updatedAtInt int64

		err := rows.Scan(
			&ve.ID,
			&ve.Text,
			&ve.Source,
			&ve.Type,
			&embeddingJSON,
			&createdAtInt,
			&updatedAtInt,
			&filePath,
			&lineNumber,
			&sessionKey,
			&tagsJSON,
			&importance,
			&accessCount,
			&lastAccessed,
			&observationKey,
			&entityMeta,
		)
		if err != nil {
			continue
		}

		// Convert timestamps
		ve.CreatedAt = time.Unix(createdAtInt, 0)
		ve.UpdatedAt = time.Unix(updatedAtInt, 0)

		if embeddingJSON.Valid {
			_ = json.Unmarshal([]byte(embeddingJSON.String), &ve.Vector)
			ve.Dimension = len(ve.Vector)
		}

		// Populate metadata fields
		ve.Metadata.FilePath = filePath.String
		ve.Metadata.LineNumber = int(lineNumber.Int64)
		ve.Metadata.SessionKey = sessionKey.String
		ve.Metadata.Importance = importance.Float64
		ve.Metadata.AccessCount = int(accessCount.Int64)
		ve.Metadata.ObservationKey = observationKey.String
		if entityMeta.Valid {
			_ = json.Unmarshal([]byte(entityMeta.String), &ve.Metadata.EntityMetadata)
		}

		if tagsJSON.Valid {
			_ = json.Unmarshal([]byte(tagsJSON.String), &ve.Metadata.Tags)
		}

		if lastAccessed.Valid {
			ve.Metadata.LastAccessed = time.Unix(lastAccessed.Int64, 0)
		}

		if filter == nil || filter(&ve) {
			results = append(results, &ve)
		}
	}

	return results, nil
}

// Close closes the store
func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop background cleanup
	if s.stopCleanup != nil {
		select {
		case <-s.stopCleanup:
			// Already closed
		default:
			close(s.stopCleanup)
		}
	}

	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// updateAccessCount updates the access count for a memory
func (s *SQLiteStore) updateAccessCount(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	_, _ = s.db.Exec(`
		UPDATE memories SET
			access_count = access_count + 1,
			last_accessed = ?
		WHERE id = ?
	`, now, id)
}

// isVectorEnabled checks if vector search is enabled
// Accepts SQLExecutor to work with both transaction and non-transaction contexts
func (s *SQLiteStore) isVectorEnabled(exec SQLExecutor) bool {
	var value string
	err := exec.QueryRow("SELECT value FROM meta WHERE key = ?", "vector_enabled").Scan(&value)
	return err == nil && value == "true"
}

// isFTSEnabled checks if FTS is enabled
// Accepts SQLExecutor to work with both transaction and non-transaction contexts
func (s *SQLiteStore) isFTSEnabled(exec SQLExecutor) bool {
	var value string
	err := exec.QueryRow("SELECT value FROM meta WHERE key = ?", "fts_enabled").Scan(&value)
	return err == nil && value == "true"
}
