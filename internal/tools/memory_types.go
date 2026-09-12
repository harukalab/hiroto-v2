package tools

import (
	"sync"
	"time"

	"github.com/hirotomasato/hiroto/internal/session"
)

// MemoryEntry represents a single extraction from one session.
type MemoryEntry struct {
	ID            string    `json:"id"`
	Content       string    `json:"content"`
	Synopsis      string    `json:"synopsis"`
	SourceSession string    `json:"source_session"`
	Phase         int       `json:"phase"`
	CreatedAt     time.Time `json:"created_at"`
}

// MemoryPipeline coordinates the two-stage memory consolidation process:
// Phase 1 extracts per-session memories, Phase 2 synthesizes consolidated output.
type MemoryPipeline struct {
	mu           sync.RWMutex
	Entries      []MemoryEntry
	Consolidated string
	Backend      MemoryBackend
	queued       map[string]bool
}

// MemoryBackend abstracts extraction and consolidation operations.
type MemoryBackend interface {
	// Extract produces a MemoryEntry from a session.
	Extract(session *session.Session) (*MemoryEntry, error)
	// Consolidate synthesizes consolidated memory text from entries.
	Consolidate(entries []*MemoryEntry) (string, error)
}

// NewMemoryPipeline creates a pipeline backed by the given backend.
func NewMemoryPipeline(backend MemoryBackend) *MemoryPipeline {
	return &MemoryPipeline{
		Entries: make([]MemoryEntry, 0),
		queued:  make(map[string]bool),
		Backend: backend,
	}
}
