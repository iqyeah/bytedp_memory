package memory

import (
	"context"
	"time"
)

// MemorySource represents where a memory entry originates
type MemorySource string

const (
	// MemorySourceLongTerm is from MEMORY.md and related files
	MemorySourceLongTerm MemorySource = "longterm"
	// MemorySourceSession is from conversation history
	MemorySourceSession MemorySource = "session"
	// MemorySourceDaily is from daily notes (YYYY-MM-DD.md)
	MemorySourceDaily MemorySource = "daily"
)

// MemoryType represents the type of memory content
type MemoryType string

const (
	// MemoryTypeFact is a factual piece of information
	// Deprecated: replaced by MemoryTypeUser / MemoryTypeFeedback / MemoryTypeReference.
	// Retained for backward compatibility with existing stored data.
	MemoryTypeFact MemoryType = "fact"
	// MemoryTypePreference is user preference or setting
	// Deprecated: replaced by MemoryTypeUser / MemoryTypeFeedback.
	MemoryTypePreference MemoryType = "preference"
	// MemoryTypeContext is situational context
	// Deprecated: replaced by the session layer (L1); no longer written.
	MemoryTypeContext MemoryType = "context"
	// MemoryTypeConversation is conversation summary
	// Deprecated: replaced by the session layer (L1); no longer written.
	MemoryTypeConversation MemoryType = "conversation"

	// MemoryTypeUser stores durable knowledge about the user's role,
	// preferences, goals, and focus areas.
	MemoryTypeUser MemoryType = "user"
	// MemoryTypeFeedback stores corrections AND confirmations the user gives
	// about how to approach work.
	MemoryTypeFeedback MemoryType = "feedback"
	// MemoryTypeReference stores pointers to external systems
	// (login portals, dashboards, data sources).
	MemoryTypeReference MemoryType = "reference"
)

// VectorEmbedding represents a vector embedding with metadata
type VectorEmbedding struct {
	ID        string         `json:"id"`
	Vector    []float32      `json:"vector"`
	Dimension int            `json:"dimension"`
	Text      string         `json:"text"`
	Source    MemorySource   `json:"source"`
	Type      MemoryType     `json:"type"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Metadata  MemoryMetadata `json:"metadata"`
}

// RelationRecord is a structured relation row linking one memory file to
// another via a wiki-link target (mirrors basic-memory's relation model,
// without the Entity indirection: the source is identified by file path).
type RelationRecord struct {
	// FilePath is the source file declaring the relation (outgoing side).
	FilePath string `json:"file_path"`
	// SourcePermalink is the stable identifier of the source memory file.
	SourcePermalink string `json:"source_permalink,omitempty"`
	// Type is the relation label ("relates_to", "implements", "links_to", …).
	Type string `json:"type"`
	// Target is the wiki-link target text as written (the target memory's
	// title or permalink).
	Target string `json:"target"`
	// Context is the trailing "(context)" supporting detail, if any.
	Context string `json:"context,omitempty"`
	// LineNumber is the 1-based line number in the source file.
	LineNumber int `json:"line_number,omitempty"`
	// MtimeMs is the source file modification time in milliseconds since epoch.
	MtimeMs int64 `json:"mtime_ms,omitempty"`
}

// EntityRecord is a graph node of the knowledge layer: a stable named
// entity (person, project, tool, system) that memory files link to via
// wiki-links. Entities are derived data: created from relation targets
// (source "link") and from gated LLM extraction (source "llm").
type EntityRecord struct {
	// Name is the normalized entity name (globally unique).
	Name string `json:"name"`
	// EntityType is an optional classification filled by the LLM channel
	// ("person", "project", "tool", …).
	EntityType string `json:"entity_type,omitempty"`
	// Summary is an optional one-line description filled by the LLM channel.
	Summary string `json:"summary,omitempty"`
	// Source records how the entity was created: "link" (wiki-link target)
	// or "llm" (gated extraction).
	Source string `json:"source,omitempty"`
	// MtimeMs is the last modification time in milliseconds since epoch.
	MtimeMs int64 `json:"mtime_ms,omitempty"`
}

// MemoryMetadata contains additional information about a memory
type MemoryMetadata struct {
	// FilePath is the source file path (if from file)
	FilePath string `json:"file_path,omitempty"`
	// LineNumber is the line in the source file
	LineNumber int `json:"line_number,omitempty"`
	// EndLineNumber is the end line for multi-line snippets
	EndLineNumber int `json:"end_line_number,omitempty"`
	// SessionKey is the session identifier (if from session)
	SessionKey string `json:"session_key,omitempty"`
	// Tags are user-defined tags
	Tags []string `json:"tags,omitempty"`
	// Importance is a user-assigned importance score (0-1)
	Importance float64 `json:"importance,omitempty"`
	// AccessCount tracks how often this memory was accessed
	AccessCount int `json:"access_count,omitempty"`
	// LastAccessed is when this memory was last retrieved
	LastAccessed time.Time `json:"last_accessed,omitempty"`
	// Timestamp is the original timestamp for session/daily memories
	Timestamp *time.Time `json:"timestamp,omitempty"`
	// Description is a one-line description used to decide relevance
	// in future conversations (mirrors the memdir frontmatter description).
	Description string `json:"description,omitempty"`
	// MtimeMs is the source file modification time in milliseconds since
	// epoch (for memdir-backed memories; used for freshness labeling).
	MtimeMs int64 `json:"mtime_ms,omitempty"`
	// Permalink is the stable identifier of the source memory file
	// (frontmatter permalink, falling back to the filename).
	Permalink string `json:"permalink,omitempty"`
	// ObservationKey is the stable hash of an observation line
	// (category+content); enables idempotent upsert of observation rows.
	ObservationKey string `json:"observation_key,omitempty"`
	// EntityMetadata holds custom frontmatter fields (key → value) for
	// structured metadata filtering.
	EntityMetadata map[string]string `json:"entity_metadata,omitempty"`
}

// SearchResult represents a memory search result with relevance score
type SearchResult struct {
	VectorEmbedding
	Score       float64 `json:"score"`
	MatchedText string  `json:"matched_text"`
	Highlight   string  `json:"highlight,omitempty"`
	// VectorScore is the pure vector similarity score (before hybrid/decay)
	VectorScore float64 `json:"vector_score,omitempty"`
	// TextScore is the keyword match score (for hybrid search)
	TextScore float64 `json:"text_score,omitempty"`
	// Citation is the formatted citation string (e.g., "memory/2024-02-15.md#L10-L20")
	Citation string `json:"citation,omitempty"`
	// AgeInDays is the age of the memory in days (for temporal decay)
	AgeInDays float64 `json:"age_in_days,omitempty"`
}

// SearchOptions configures memory search behavior
type SearchOptions struct {
	// Limit is the maximum number of results to return
	Limit int `json:"limit"`
	// MinScore is the minimum similarity score (0-1)
	MinScore float64 `json:"min_score"`
	// Sources filters which memory sources to search
	Sources []MemorySource `json:"sources,omitempty"`
	// Types filters which memory types to search
	Types []MemoryType `json:"types,omitempty"`
	// TextQuery enables FTS5 full-text search (CJK-aware with trigram tokenizer).
	// When non-empty, bypasses vector embedding and searches by keyword.
	TextQuery string `json:"text_query,omitempty"`
	// Hybrid enables hybrid vector+keyword search
	Hybrid bool `json:"hybrid"`
	// VectorWeight is the weight for vector similarity in hybrid search (0-1)
	VectorWeight float64 `json:"vector_weight"`
	// TextWeight is the weight for keyword match in hybrid search (0-1)
	TextWeight float64 `json:"text_weight"`
	// MMR enables Maximal Marginal Relevance re-ranking for diversity
	MMR *MMRConfig `json:"mmr,omitempty"`
	// TemporalDecay enables time-aware scoring
	TemporalDecay *TemporalDecayConfig `json:"temporal_decay,omitempty"`
	// IncludeCitations adds citation strings to results
	IncludeCitations bool `json:"include_citations,omitempty"`
	// SessionKeys filters results to memories belonging to the given
	// sessions. Empty means no session filtering (global + all sessions).
	SessionKeys []string `json:"session_keys,omitempty"`
	// AfterDate filters results to memories updated at or after this time
	// (natural-language time windows via ParseTimeframe).
	AfterDate *time.Time `json:"after_date,omitempty"`
	// MetadataFilters keeps only results whose metadata matches all given
	// field-value pairs (post-filter in Go, avoids SQL JSON compatibility issues).
	MetadataFilters map[string]string `json:"metadata_filters,omitempty"`
	// RelationType filters results to memories that declare at least one
	// outgoing relation of the given type (e.g. "implements").
	RelationType string `json:"relation_type,omitempty"`
	// RelationTarget filters results to memories that declare a relation
	// pointing at the given target. The target may be the wiki-link target
	// as written, a memory permalink, or a memory name (resolved against
	// indexed metadata).
	RelationTarget string `json:"relation_target,omitempty"`
	// Trace, when non-nil, receives retrieval diagnostics for this search.
	Trace *SearchTrace `json:"-"`
}

// SearchTrace records retrieval pipeline diagnostics for a single search.
type SearchTrace struct {
	// Mode is the retrieval mode used: "fts", "like", "vector", or "hybrid".
	Mode string `json:"mode"`
	// RelaxedTriggered reports whether the relaxed FTS fallback fired
	// (strict query returned no hits, retried with OR-expanded/prefix query).
	RelaxedTriggered bool `json:"relaxed_triggered"`
	// LikeFallback reports whether the LIKE substring fallback fired
	// (query too short for FTS5 trigram matching).
	LikeFallback bool `json:"like_fallback"`
	// Hits is the number of raw hits returned by the store before
	// post-processing (decay/MMR/min-score/limit).
	Hits int `json:"hits"`
	// Duration is the time spent inside the store search call.
	Duration time.Duration `json:"duration"`
}

// DefaultSearchOptions returns sensible default search options
func DefaultSearchOptions() SearchOptions {
	return SearchOptions{
		Limit:            10,
		MinScore:         0.7,
		Sources:          nil, // All sources
		Types:            nil, // All types
		Hybrid:           true,
		VectorWeight:     0.7,
		TextWeight:       0.3,
		IncludeCitations: true,
	}
}

// MMRConfig configures Maximal Marginal Relevance re-ranking
type MMRConfig struct {
	// Enabled enables MMR re-ranking
	Enabled bool `json:"enabled"`
	// Lambda balances relevance vs diversity: 0=max diversity, 1=max relevance
	Lambda float64 `json:"lambda"`
}

// DefaultMMRConfig returns default MMR configuration
func DefaultMMRConfig() MMRConfig {
	return MMRConfig{
		Enabled: false,
		Lambda:  0.7,
	}
}

// TemporalDecayConfig configures time-aware scoring decay
type TemporalDecayConfig struct {
	// Enabled enables temporal decay
	Enabled bool `json:"enabled"`
	// HalfLifeDays is the half-life for exponential decay (default: 30 days)
	HalfLifeDays float64 `json:"half_life_days"`
}

// DefaultTemporalDecayConfig returns default temporal decay configuration
func DefaultTemporalDecayConfig() TemporalDecayConfig {
	return TemporalDecayConfig{
		Enabled:      false,
		HalfLifeDays: 30,
	}
}

// EmbeddingProvider defines the interface for generating embeddings
type EmbeddingProvider interface {
	// Embed generates a single embedding
	Embed(text string) ([]float32, error)
	// EmbedWithContext generates a single embedding with context support for cancellation
	EmbedWithContext(ctx context.Context, text string) ([]float32, error)
	// EmbedBatch generates multiple embeddings in one call
	EmbedBatch(texts []string) ([][]float32, error)
	// EmbedBatchWithContext generates multiple embeddings with context support for cancellation
	EmbedBatchWithContext(ctx context.Context, texts []string) ([][]float32, error)
	// Dimension returns the dimension of embeddings
	Dimension() int
	// MaxBatchSize returns the maximum batch size
	MaxBatchSize() int
	// VecTableName returns the model-specific vector table name
	VecTableName() string
}

// Store defines the interface for memory storage
type Store interface {
	// Add adds a memory to the store
	Add(embedding *VectorEmbedding) error
	// AddBatch adds multiple memories in one transaction
	AddBatch(embeddings []*VectorEmbedding) error
	// Search performs similarity search
	Search(query []float32, opts SearchOptions) ([]*SearchResult, error)
	// Get retrieves a memory by ID
	Get(id string) (*VectorEmbedding, error)
	// Delete removes a memory by ID
	Delete(id string) error
	// Update updates an existing memory
	Update(embedding *VectorEmbedding) error
	// List lists all memories with optional filtering
	List(filter func(*VectorEmbedding) bool) ([]*VectorEmbedding, error)
	// Close closes the store
	Close() error
}
