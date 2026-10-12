package memory

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// UpsertEntities upserts entity rows keyed by name. The "llm" channel
// refreshes entity_type/summary on conflict (without clearing them when
// the new payload omits them); the "link" channel only ensures the entity
// exists and never downgrades richer data.
func (s *SQLiteStore) UpsertEntities(entities []EntityRecord, mtimeMs int64) error {
	if len(entities) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, e := range entities {
		e.Name = strings.TrimSpace(e.Name)
		if e.Name == "" {
			continue
		}
		if e.Source == "" {
			e.Source = "link"
		}
		if e.Source == "llm" {
			if _, err := tx.Exec(`
				INSERT INTO entities (name, entity_type, summary, source, mtime_ms)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(name) DO UPDATE SET
					entity_type = CASE WHEN excluded.entity_type != ''
						THEN excluded.entity_type ELSE entities.entity_type END,
					summary = CASE WHEN excluded.summary != ''
						THEN excluded.summary ELSE entities.summary END,
					source = excluded.source,
					mtime_ms = excluded.mtime_ms
			`, e.Name, e.EntityType, e.Summary, e.Source, mtimeMs); err != nil {
				return fmt.Errorf("failed to upsert entity %q: %w", e.Name, err)
			}
			continue
		}
		// Link channel: only ensure presence, keep existing data untouched.
		if _, err := tx.Exec(`
			INSERT INTO entities (name, entity_type, summary, source, mtime_ms)
			VALUES (?, '', '', 'link', ?)
			ON CONFLICT(name) DO NOTHING
		`, e.Name, mtimeMs); err != nil {
			return fmt.Errorf("failed to ensure entity %q: %w", e.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit entities: %w", err)
	}
	return nil
}

// ListEntities returns entities ordered by mtime descending.
func (s *SQLiteStore) ListEntities(limit int) ([]EntityRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT name, entity_type, summary, source, mtime_ms
		FROM entities
		ORDER BY mtime_ms DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list entities: %w", err)
	}
	defer rows.Close()

	var out []EntityRecord
	for rows.Next() {
		var e EntityRecord
		if err := rows.Scan(&e.Name, &e.EntityType, &e.Summary, &e.Source, &e.MtimeMs); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEntity returns one entity by exact name, or nil when absent.
func (s *SQLiteStore) GetEntity(name string) (*EntityRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`
		SELECT name, entity_type, summary, source, mtime_ms
		FROM entities
		WHERE name = ?
	`, name)
	var e EntityRecord
	err := row.Scan(&e.Name, &e.EntityType, &e.Summary, &e.Source, &e.MtimeMs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get entity: %w", err)
	}
	return &e, nil
}

// DeleteEntity removes an entity by name. Relations referencing it are
// left untouched (no cascade); the entity reappears on the next link sync.
func (s *SQLiteStore) DeleteEntity(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Exec(`DELETE FROM entities WHERE name = ?`, name); err != nil {
		return fmt.Errorf("failed to delete entity: %w", err)
	}
	return nil
}

// ListEntityRelations returns the entity's relation edges. incoming=true
// returns relations pointing at the entity (with alias resolution);
// incoming=false returns relations declared by the entity's memory file
// (matched by source permalink).
func (s *SQLiteStore) ListEntityRelations(name string, incoming bool) ([]RelationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if incoming {
		candidates := s.resolveRelationTarget(name)
		if len(candidates) == 0 {
			return nil, nil
		}
		placeholders := make([]string, len(candidates))
		args := make([]interface{}, len(candidates))
		for i, c := range candidates {
			placeholders[i] = "?"
			args[i] = c
		}
		return s.queryRelations(
			`WHERE target IN (`+joinString(placeholders, ",")+`) ORDER BY mtime_ms DESC`,
			args...,
		)
	}
	return s.queryRelations(`WHERE source_permalink = ? ORDER BY mtime_ms DESC`, name)
}

// fuzzyEntityCandidates returns entity names matching raw by prefix or
// substring, ordered by match quality (prefix tier first, then substring),
// capped at 5. Callers must hold the store read lock.
func (s *SQLiteStore) fuzzyEntityCandidates(raw string) []string {
	rows, err := s.db.Query(`SELECT name FROM entities`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names = append(names, n)
		}
	}

	var prefixes, substrings []string
	for _, n := range names {
		if n == raw {
			continue // exact match is already covered by the raw target
		}
		if strings.HasPrefix(n, raw) {
			prefixes = append(prefixes, n)
		} else if strings.Contains(n, raw) || strings.Contains(raw, n) {
			substrings = append(substrings, n)
		}
	}
	sort.Strings(prefixes)
	sort.Strings(substrings)

	out := append(prefixes, substrings...)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}
