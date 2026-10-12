// Package memory provides an agent memory retrieval system built on SQLite:
// FTS5 full-text search (trigram tokenizer, CJK-aware), optional sqlite-vec
// vector similarity search, hybrid retrieval, MMR reranking, temporal decay,
// TTL governance, and LRU caching.
//
// The typical entry point is MemoryManager (embedding generation + retrieval
// orchestration), backed by SQLiteStore for persistence. The memdir subpackage
// implements the "file as source of truth" long-term memory layer with three
// memory kinds: user / feedback / reference.
package memory
