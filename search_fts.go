package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// sanitizeFTS5Query escapes FTS5 special characters while preserving
// CJK text and double-quoted phrases. Boolean operators AND/OR/NOT and
// parentheses are whitelisted for grouped queries; other operator symbols
// are removed. Returns empty string if the query is too short for trigram.
func sanitizeFTS5Query(query string) string {
	var b strings.Builder
	for _, r := range query {
		switch r {
		case '*', '+', '-', '^', '~', ':', '=', '<', '>':
			// Remove FTS5 operator symbols we don't support.
			continue
		default:
			b.WriteRune(r)
		}
	}
	sanitized := normalizeBooleanOperators(strings.TrimSpace(b.String()))

	// Trigram tokenizer requires at least 3 characters to match.
	if len([]rune(sanitized)) < 3 {
		return "" // caller should fallback to LIKE
	}
	return sanitized
}

// normalizeBooleanOperators keeps whitelisted boolean operators (AND/OR/NOT)
// only in valid binary positions. Operators in invalid positions and the
// unsupported NEAR operator are quoted so they match as literal phrases.
func normalizeBooleanOperators(s string) string {
	tokens := splitFTSQueryTokens(s)
	out := make([]string, 0, len(tokens))
	for i, t := range tokens {
		if !isFTSBoolOperator(t) {
			out = append(out, t)
			continue
		}
		if t == "NEAR" {
			out = append(out, `"NEAR"`)
			continue
		}
		prevOK := i > 0 && !isFTSBoolOperator(tokens[i-1])
		nextOK := i+1 < len(tokens) && !isFTSBoolOperator(tokens[i+1])
		if prevOK && nextOK {
			out = append(out, t)
		} else {
			out = append(out, `"`+t+`"`)
		}
	}
	return strings.Join(out, " ")
}

// isFTSBoolOperator reports whether token is an FTS5 boolean keyword.
func isFTSBoolOperator(t string) bool {
	return t == "AND" || t == "OR" || t == "NOT" || t == "NEAR"
}

// splitFTSQueryTokens splits a query into tokens, keeping double-quoted
// phrases intact and parentheses as standalone tokens.
func splitFTSQueryTokens(s string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case (r == '(' || r == ')') && !inQuote:
			flush()
			tokens = append(tokens, string(r))
		case r == ' ' && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// buildRelaxedFTSQuery expands a query for a relaxed retry when the strict
// match returns nothing: multiple terms become OR-connected phrases, and a
// single term becomes a prefix match ("term*").
func buildRelaxedFTSQuery(textQuery string) string {
	fields := strings.Fields(textQuery)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) == 1 {
		term := strings.Trim(fields[0], `"`)
		if term == "" {
			return ""
		}
		return `"` + term + `"*`
	}
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = `"` + strings.Trim(f, `"`) + `"`
	}
	return strings.Join(quoted, " OR ")
}

// hasMinimumTrigramQuery reports whether the query has enough characters
// for FTS5 trigram matching (>= 3 runes).
func hasMinimumTrigramQuery(query string) bool {
	return len([]rune(strings.TrimSpace(query))) >= 3
}

// searchFTS performs full-text search (legacy, uses vector as placeholder).
// Kept for hybrid compatibility.
func (s *SQLiteStore) searchFTS(query []float32, opts SearchOptions) ([]*SearchResult, error) {
	// Placeholder: full-text needs a text query not a vector.
	// Use searchFTSByText for actual keyword search.
	return []*SearchResult{}, nil
}

// searchFTSByText performs BM25 full-text search using FTS5 trigram tokenizer.
// Supports CJK natively. Falls back to a relaxed OR-expanded/prefix query when
// the strict match returns nothing, and to LIKE substring search when the
// query is too short for trigram matching.
func (s *SQLiteStore) searchFTSByText(textQuery string, opts SearchOptions) ([]*SearchResult, error) {
	sanitized := sanitizeFTS5Query(textQuery)
	if sanitized == "" {
		// Query too short for trigram — LIKE substring fallback.
		if opts.Trace != nil {
			opts.Trace.LikeFallback = true
			opts.Trace.Mode = "like"
		}
		return s.searchLike(textQuery, opts)
	}

	results, err := s.searchFTSByQuery(sanitized, opts)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		relaxed := buildRelaxedFTSQuery(textQuery)
		if relaxed != "" && relaxed != sanitized {
			if opts.Trace != nil {
				opts.Trace.RelaxedTriggered = true
			}
			if r2, err := s.searchFTSByQuery(relaxed, opts); err == nil && len(r2) > 0 {
				return r2, nil
			}
		}
	}
	return results, nil
}

// searchFTSByQuery runs a prepared MATCH query against the FTS5 table.
func (s *SQLiteStore) searchFTSByQuery(matchQuery string, opts SearchOptions) ([]*SearchResult, error) {
	// Build MATCH query with source/type filters via JOIN to memories.
	sessionFilter, sessionArgs := sessionKeyFilter(opts.SessionKeys)
	afterFilter, afterArgs := afterDateFilter(opts.AfterDate)
	relFilter, relArgs := relationFilter(opts.RelationType, s.resolveRelationTarget(opts.RelationTarget))
	querySQL := `
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
			rank
		FROM memory_fts f
		JOIN memories m ON m.id = f.id
		WHERE memory_fts MATCH ?
		AND m.source IN (` + sourcePlaceholders(opts.Sources) + `)
		AND m.type IN (` + typePlaceholders(opts.Types) + `)
		` + sessionFilter + afterFilter + relFilter + `
		ORDER BY rank
		LIMIT ?
	`

	args := []interface{}{matchQuery}
	args = appendSources(args, opts.Sources)
	args = appendTypes(args, opts.Types)
	args = append(args, sessionArgs...)
	args = append(args, afterArgs...)
	args = append(args, relArgs...)
	args = append(args, opts.Limit)

	rows, err := s.db.Query(querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("FTS5 query failed: %w", err)
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

		// Populate metadata fields.
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

		// Normalize BM25 rank to 0-1 score.
		// FTS5 rank is a positive float (lower = better).
		// Convert to similarity: 1 / (1 + rank).
		if sr.Score > 0 {
			sr.Score = 1.0 / (1.0 + sr.Score)
		} else {
			sr.Score = 0.9 // exact match fallback
		}

		if sr.Score >= opts.MinScore {
			results = append(results, &sr)
		}
	}

	return results, rows.Err()
}

// searchLike performs a LIKE substring search on memories.text as a fallback
// for queries too short for FTS5 trigram matching.
func (s *SQLiteStore) searchLike(textQuery string, opts SearchOptions) ([]*SearchResult, error) {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(textQuery)
	sessionFilter, sessionArgs := sessionKeyFilter(opts.SessionKeys)
	afterFilter, afterArgs := afterDateFilter(opts.AfterDate)
	relFilter, relArgs := relationFilter(opts.RelationType, s.resolveRelationTarget(opts.RelationTarget))

	querySQL := `
		SELECT
			m.id, m.text, m.source, m.type, m.created_at, m.updated_at,
			m.file_path, m.line_number, m.session_key, m.tags, m.importance,
			m.access_count, m.last_accessed,
			m.observation_key, m.entity_metadata
		FROM memories m
		WHERE m.text LIKE ? ESCAPE '\'
		AND m.source IN (` + sourcePlaceholders(opts.Sources) + `)
		AND m.type IN (` + typePlaceholders(opts.Types) + `)
		` + sessionFilter + afterFilter + relFilter + `
		ORDER BY m.updated_at DESC
		LIMIT ?
	`

	args := []interface{}{"%" + escaped + "%"}
	args = appendSources(args, opts.Sources)
	args = appendTypes(args, opts.Types)
	args = append(args, sessionArgs...)
	args = append(args, afterArgs...)
	args = append(args, relArgs...)
	args = append(args, opts.Limit)

	rows, err := s.db.Query(querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("LIKE query failed: %w", err)
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
		)
		if err != nil {
			continue
		}

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

		// Fixed moderate score for substring matches.
		sr.Score = 0.5
		if sr.Score >= opts.MinScore {
			results = append(results, &sr)
		}
	}

	return results, rows.Err()
}
