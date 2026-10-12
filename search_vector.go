package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// searchVector performs vector similarity search
func (s *SQLiteStore) searchVector(query []float32, opts SearchOptions) ([]*SearchResult, error) {
	queryStr := float32SliceToString(query)

	sessionFilter, sessionArgs := sessionKeyFilter(opts.SessionKeys)
	afterFilter, afterArgs := afterDateFilter(opts.AfterDate)
	relFilter, relArgs := relationFilter(opts.RelationType, s.resolveRelationTarget(opts.RelationTarget))

	querySQL := fmt.Sprintf(`
		SELECT
			m.id,
			m.text,
			m.source,
			m.type,
			m.created_at,
			m.updated_at,
			m.file_path,
			m.line_number,
			m.session_key,
			m.tags,
			m.importance,
			m.access_count,
			m.last_accessed,
			m.observation_key,
			m.entity_metadata,
			distance
		FROM %s v
		JOIN memories m ON m.id = v.id
		WHERE v.embedding MATCH ? AND k = ?
		AND m.source IN (`+sourcePlaceholders(opts.Sources)+`)
		AND m.type IN (`+typePlaceholders(opts.Types)+`)
		%s%s%s
		ORDER BY distance
		LIMIT ?
	`, s.vecTableName, sessionFilter, afterFilter, relFilter)

	// k is the KNN candidate count required by sqlite-vec for joined
	// queries; over-fetch so post-filters still have room to prune.
	k := opts.Limit * 4
	if k < 16 {
		k = 16
	}

	args := []interface{}{queryStr, k}
	args = appendSources(args, opts.Sources)
	args = appendTypes(args, opts.Types)
	args = append(args, sessionArgs...)
	args = append(args, afterArgs...)
	args = append(args, relArgs...)
	args = append(args, opts.Limit)

	rows, err := s.db.Query(querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var results []*SearchResult
	for rows.Next() {
		var sr SearchResult
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
			&sr.ID,
			&sr.Text,
			&sr.Source,
			&sr.Type,
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
			&sr.Score,
		)
		if err != nil {
			continue
		}

		// Populate metadata fields
		sr.CreatedAt = time.Unix(createdAtInt, 0)
		sr.UpdatedAt = time.Unix(updatedAtInt, 0)
		sr.Metadata.FilePath = filePath.String
		sr.Metadata.LineNumber = int(lineNumber.Int64)
		sr.Metadata.SessionKey = sessionKey.String
		sr.Metadata.Importance = importance.Float64
		sr.Metadata.AccessCount = int(accessCount.Int64)
		sr.Metadata.ObservationKey = observationKey.String
		if entityMeta.Valid {
			_ = json.Unmarshal([]byte(entityMeta.String), &sr.Metadata.EntityMetadata)
		}

		if lastAccessed.Valid {
			sr.Metadata.LastAccessed = time.Unix(lastAccessed.Int64, 0)
		}

		if tagsJSON.Valid {
			_ = json.Unmarshal([]byte(tagsJSON.String), &sr.Metadata.Tags)
		}

		// Convert distance to similarity score (lower distance = higher score)
		// Assuming L2 distance, convert to 0-1 range
		sr.Score = 1.0 / (1.0 + sr.Score)

		if sr.Score >= opts.MinScore {
			results = append(results, &sr)
		}
	}

	return results, nil
}

// mergeHybridResults combines vector and FTS results
func (s *SQLiteStore) mergeHybridResults(vectorResults, ftsResults []*SearchResult, opts SearchOptions) []*SearchResult {
	// Create a map of results by ID
	resultMap := make(map[string]*SearchResult)

	// Add vector results
	for _, vr := range vectorResults {
		resultMap[vr.ID] = vr
	}

	// Merge FTS results
	for _, fr := range ftsResults {
		if existing, ok := resultMap[fr.ID]; ok {
			// Combine scores using weighted average
			existing.Score = existing.Score*opts.VectorWeight + fr.Score*opts.TextWeight
		} else {
			fr.Score = fr.Score * opts.TextWeight
			resultMap[fr.ID] = fr
		}
	}

	// Convert back to slice and sort by score
	var results []*SearchResult
	for _, r := range resultMap {
		if r.Score >= opts.MinScore {
			results = append(results, r)
		}
	}

	// Sort by score descending
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[i].Score < results[j].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit results
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results
}
