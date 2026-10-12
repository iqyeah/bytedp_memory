package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// SaveRelations upserts structured relation rows for one source file.
// The unique key is (file_path, relation_type, target, context); a
// conflicting row refreshes source_permalink, line_number, and mtime_ms.
func (s *SQLiteStore) SaveRelations(filePath, sourcePermalink string, relations []RelationRecord, mtimeMs int64) error {
	if len(relations) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range relations {
		r.FilePath = filePath
		r.SourcePermalink = sourcePermalink
		r.MtimeMs = mtimeMs
		if _, err := tx.Exec(`
			INSERT INTO relations (
				file_path, source_permalink, relation_type, target,
				context, line_number, mtime_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(file_path, relation_type, target, context) DO UPDATE SET
				source_permalink = excluded.source_permalink,
				line_number = excluded.line_number,
				mtime_ms = excluded.mtime_ms
		`, r.FilePath, r.SourcePermalink, r.Type, r.Target, r.Context, r.LineNumber, r.MtimeMs); err != nil {
			return fmt.Errorf("failed to upsert relation: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit relations: %w", err)
	}
	return nil
}

// DeleteRelationsByFile removes every relation row declared by a file.
// Used by the indexer before rebuilding a file's relations and when a
// file disappears.
func (s *SQLiteStore) DeleteRelationsByFile(filePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Exec(`DELETE FROM relations WHERE file_path = ?`, filePath); err != nil {
		return fmt.Errorf("failed to delete relations: %w", err)
	}
	return nil
}

// ListOutgoing returns all relations declared by the given memory file.
func (s *SQLiteStore) ListOutgoing(filePath string) ([]RelationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.queryRelations(`WHERE file_path = ? ORDER BY line_number`, filePath)
}

// ListIncoming returns all relations pointing at the given memory
// identifier. The identifier may be the wiki-link target as written (a
// title or a permalink), a memory permalink, or a memory name; matching
// resolves the aliases so links written either way are found.
func (s *SQLiteStore) ListIncoming(target string) ([]RelationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	candidates := s.resolveRelationTarget(target)
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

// Traverse expands the relation graph from a start entity name up to hops
// levels, returning the deduplicated edge set. startName matches a
// relation target (with alias resolution) or a source permalink. When
// outgoingOnly is true only out-edges (source_permalink = node) are
// followed; otherwise the walk is bidirectional.
func (s *SQLiteStore) Traverse(startName string, hops int, outgoingOnly bool) ([]RelationRecord, error) {
	if hops <= 0 {
		hops = 1
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var edges []RelationRecord
	seen := make(map[string]bool)
	visited := make(map[string]bool)

	// addRows deduplicates rows by edge identity and returns the next
	// frontier: targets when following out-edges, source permalinks when
	// walking upstream along in-edges.
	addRows := func(rows []RelationRecord, followTarget bool) []string {
		var next []string
		for _, r := range rows {
			key := r.FilePath + "|" + r.Type + "|" + r.Target + "|" + r.Context
			if seen[key] {
				continue
			}
			seen[key] = true
			edges = append(edges, r)
			if followTarget {
				next = append(next, r.Target)
			} else if r.SourcePermalink != "" {
				next = append(next, r.SourcePermalink)
			}
		}
		return next
	}

	frontier := []string{startName}
	for level := 0; level < hops; level++ {
		var next []string
		for _, node := range frontier {
			if node == "" || visited[node] {
				continue
			}
			visited[node] = true

			// In-edges: relations whose target resolves to node. Skipped for
			// outgoing-only walks.
			if !outgoingOnly {
				candidates := s.resolveRelationTarget(node)
				placeholders := make([]string, len(candidates))
				args := make([]interface{}, len(candidates))
				for i, c := range candidates {
					placeholders[i] = "?"
					args[i] = c
				}
				inRows, err := s.queryRelations(
					`WHERE target IN (`+joinString(placeholders, ",")+`)`,
					args...,
				)
				if err != nil {
					return nil, err
				}
				next = append(next, addRows(inRows, false)...)
			}

			// Out-edges: relations declared by the node's memory file.
			outRows, err := s.queryRelations(`WHERE source_permalink = ?`, node)
			if err != nil {
				return nil, err
			}
			next = append(next, addRows(outRows, true)...)
		}
		if len(next) == 0 {
			break
		}
		frontier = next
	}
	return edges, nil
}

// queryRelations scans relation rows for the given WHERE fragment. Callers
// must hold the store read lock.
func (s *SQLiteStore) queryRelations(where string, args ...interface{}) ([]RelationRecord, error) {
	rows, err := s.db.Query(`
		SELECT file_path, source_permalink, relation_type, target,
			context, line_number, mtime_ms
		FROM relations
	`+where, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query relations: %w", err)
	}
	defer rows.Close()

	var results []RelationRecord
	for rows.Next() {
		var r RelationRecord
		if err := rows.Scan(
			&r.FilePath, &r.SourcePermalink, &r.Type, &r.Target,
			&r.Context, &r.LineNumber, &r.MtimeMs,
		); err != nil {
			continue
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// resolveRelationTarget expands a raw relation target into the set of texts
// that may appear in relations.target for the same memory: the raw target
// plus the name and permalink aliases of every indexed memory whose name
// or permalink equals the raw target. Aliases are read from the persisted
// entity_metadata JSON (name/permalink keys written by the indexer).
// Callers must hold the store read lock.
func (s *SQLiteStore) resolveRelationTarget(raw string) []string {
	if raw == "" {
		return nil
	}
	candidates := map[string]bool{raw: true}

	rows, err := s.db.Query(`
		SELECT entity_metadata FROM memories
		WHERE entity_metadata IS NOT NULL
	`)
	if err != nil {
		return []string{raw}
	}
	defer rows.Close()

	for rows.Next() {
		var entityMeta sql.NullString
		if err := rows.Scan(&entityMeta); err != nil {
			continue
		}
		if !entityMeta.Valid {
			continue
		}
		var em map[string]string
		if json.Unmarshal([]byte(entityMeta.String), &em) != nil {
			continue
		}
		name := em["name"]
		permalink := em["permalink"]
		matched := name == raw || permalink == raw
		if !matched {
			continue
		}
		if permalink != "" {
			candidates[permalink] = true
		}
		if name != "" {
			candidates[name] = true
		}
	}

	// Fuzzy fallback: when no alias matched (only the raw target remains),
	// extend the candidate set with entity names close to the raw target
	// (prefix/substring), so slightly-off wiki-links still resolve.
	if len(candidates) == 1 {
		for _, c := range s.fuzzyEntityCandidates(raw) {
			candidates[c] = true
		}
	}

	out := make([]string, 0, len(candidates))
	for c := range candidates {
		out = append(out, c)
	}
	return out
}
