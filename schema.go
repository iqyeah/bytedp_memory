package bytedp_memory

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// schemaMigrations is the ordered schema migration sequence applied at
// startup. Every statement is idempotent (IF NOT EXISTS) so databases that
// already ran a step converge; the version watermark lives in the meta
// table. v1 = memories + indexes, v2 = relations + indexes, v3 = entities.
var schemaMigrations = []struct {
	version int
	name    string
	stmts   []string
}{
	{
		version: 1,
		name:    "memories",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS memories (
				id TEXT PRIMARY KEY,
				text TEXT NOT NULL,
				source TEXT NOT NULL,
				type TEXT NOT NULL,
				embedding BLOB,
				dimension INTEGER,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL,
				file_path TEXT,
				line_number INTEGER,
				session_key TEXT,
				tags TEXT,
				importance REAL DEFAULT 0.5,
				access_count INTEGER DEFAULT 0,
				last_accessed INTEGER,
				observation_key TEXT,
				entity_metadata TEXT
			)`,
			`CREATE INDEX IF NOT EXISTS idx_memories_source
				ON memories(source)`,
			`CREATE INDEX IF NOT EXISTS idx_memories_type
				ON memories(type)`,
			`CREATE INDEX IF NOT EXISTS idx_memories_created_at
				ON memories(created_at)`,
			// Unique index on observation_key for idempotent observation-line
			// upserts. SQLite treats NULLs as distinct, so legacy rows without
			// an observation key never conflict.
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_memories_observation_key
				ON memories(observation_key)`,
		},
	},
	{
		version: 2,
		name:    "relations",
		stmts: []string{
			// Relations table: structured projection of wiki-link relation lines
			// ("- type [[Target]]"). Rows are rebuilt per file by the indexer and
			// keyed by (file_path, relation_type, target, context).
			`CREATE TABLE IF NOT EXISTS relations (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				file_path TEXT NOT NULL,
				source_permalink TEXT NOT NULL DEFAULT '',
				relation_type TEXT NOT NULL,
				target TEXT NOT NULL,
				context TEXT NOT NULL DEFAULT '',
				line_number INTEGER NOT NULL DEFAULT 0,
				mtime_ms INTEGER NOT NULL DEFAULT 0,
				UNIQUE(file_path, relation_type, target, context)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_relations_target
				ON relations(target)`,
			`CREATE INDEX IF NOT EXISTS idx_relations_type
				ON relations(relation_type)`,
			`CREATE INDEX IF NOT EXISTS idx_relations_file_path
				ON relations(file_path)`,
		},
	},
	{
		version: 3,
		name:    "entities",
		stmts: []string{
			// Entities table: the graph's nodes. Created from wiki-link targets
			// (source='link') and from gated LLM extraction (source='llm').
			// Entities are globally persistent: they are not cleaned up when a
			// file disappears (explicit DeleteEntity only).
			`CREATE TABLE IF NOT EXISTS entities (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				entity_type TEXT NOT NULL DEFAULT '',
				summary TEXT NOT NULL DEFAULT '',
				source TEXT NOT NULL DEFAULT 'link',
				mtime_ms INTEGER NOT NULL DEFAULT 0,
				UNIQUE(name)
			)`,
		},
	},
}

// initSchema creates the database schema
func (s *SQLiteStore) initSchema(config StoreConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Enable foreign keys
	if _, err := s.db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := s.db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// Create metadata table
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create meta table: %w", err)
	}

	// Apply versioned schema migrations (memories → relations → entities).
	if err := s.runMigrations(); err != nil {
		return err
	}

	// Add columns introduced after the initial schema to databases created
	// before them (column-level compatibility, independent of the versioned
	// migrations above). Existing rows keep NULL values, which the unique
	// observation_key index naturally allows (SQLite treats NULLs as distinct).
	if err := s.migrateMemoryColumns(); err != nil {
		return err
	}

	// Enable vector search if configured
	if config.EnableVectorSearch {
		if err := s.initVectorSearch(); err != nil {
			// Log warning but don't fail - vector search is optional
			fmt.Printf("Warning: vector search initialization failed: %v\n", err)
		}
	}

	// Enable FTS if configured
	if config.EnableFTS {
		if err := s.initFTS(); err != nil {
			// Log warning but don't fail - FTS is optional
			fmt.Printf("Warning: FTS initialization failed: %v\n", err)
		}
	}

	if s.provider != nil {
		s.setMeta("provider_dimension", fmt.Sprintf("%d", s.provider.Dimension()))
	}

	return nil
}

// migrateMemoryColumns adds columns introduced after the initial schema
// (observation_key, entity_metadata) to existing databases. Fresh databases
// already have them from CREATE TABLE, so the ALTERs are skipped.
func (s *SQLiteStore) migrateMemoryColumns() error {
	cols := make(map[string]bool)
	rows, err := s.db.Query(`PRAGMA table_info(memories)`)
	if err != nil {
		return fmt.Errorf("failed to inspect memories table: %w", err)
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan table_info: %w", err)
		}
		cols[name] = true
	}
	rows.Close()

	for _, col := range []string{"observation_key", "entity_metadata"} {
		if cols[col] {
			continue
		}
		if _, err := s.db.Exec(`ALTER TABLE memories ADD COLUMN ` + col + ` TEXT`); err != nil {
			return fmt.Errorf("failed to add column %s: %w", col, err)
		}
	}
	return nil
}

// initVectorSearch initializes vector similarity search using sqlite-vec.
// The vec0 module is auto-registered by blank-importing modernc.org/sqlite/vec
// (CGo-free bundled extension), so no load_extension step is needed here.
func (s *SQLiteStore) initVectorSearch() error {
	var dimension int = 1536 // default
	if s.provider != nil {
		dimension = s.provider.Dimension()
	}

	// Create virtual table for vector search
	_, err := s.db.Exec(fmt.Sprintf(`
		CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(
			id TEXT PRIMARY KEY,
			embedding FLOAT[%d]
		)
	`, s.vecTableName, dimension))

	if err != nil {
		return fmt.Errorf("failed to create vec0 table: %w", err)
	}

	s.setMeta("vector_enabled", "true")
	return nil
}

// initFTS initializes full-text search using FTS5 with trigram tokenizer
// for CJK (Chinese/Japanese/Korean) support.
func (s *SQLiteStore) initFTS() error {
	// Check if the existing FTS5 table uses the old default tokenizer (not trigram).
	// If so, DROP it and recreate with trigram + reindex.
	var createSQL string
	s.db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='memory_fts'").Scan(&createSQL)
	needRebuild := createSQL != "" && !strings.Contains(createSQL, "tokenize='trigram'")

	if needRebuild {
		if _, err := s.db.Exec("DROP TABLE IF EXISTS memory_fts"); err != nil {
			return fmt.Errorf("failed to drop old FTS5 table: %w", err)
		}
	}

	_, err := s.db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
			text,
			id UNINDEXED,
			source UNINDEXED,
			type UNINDEXED,
			tokenize='trigram'
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create FTS5 table: %w", err)
	}

	// Reindex historical data if table was rebuilt.
	if needRebuild {
		if _, err := s.db.Exec(`
			INSERT INTO memory_fts (text, id, source, type)
			SELECT text, id, source, type FROM memories
		`); err != nil {
			// Log warning but don't fail — FTS is optional.
			fmt.Printf("Warning: FTS5 reindex failed: %v\n", err)
		}
	}

	s.setMeta("fts_enabled", "true")
	return nil
}

// setMeta stores a key-value pair in the metadata table
func (s *SQLiteStore) setMeta(key, value string) {
	now := time.Now().Unix()
	_, _ = s.db.Exec(`
		INSERT INTO meta (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, value, now)
}

// runMigrations applies every migration newer than the recorded schema
// version, advancing the watermark after each step. Callers must hold the
// store write lock.
func (s *SQLiteStore) runMigrations() error {
	current := s.schemaVersion()
	for _, m := range schemaMigrations {
		if m.version <= current {
			continue
		}
		for _, stmt := range m.stmts {
			if _, err := s.db.Exec(stmt); err != nil {
				return fmt.Errorf("migration v%d (%s): %w", m.version, m.name, err)
			}
		}
		s.setMeta("schema_version", strconv.Itoa(m.version))
	}
	return nil
}

// schemaVersion reads the recorded migration watermark (0 when absent).
func (s *SQLiteStore) schemaVersion() int {
	var v string
	_ = s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v)
	n, _ := strconv.Atoi(v)
	return n
}
