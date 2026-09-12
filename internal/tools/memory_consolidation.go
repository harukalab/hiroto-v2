package tools

import (
	"fmt"
	"sort"
	"time"

	"github.com/hirotomasato/hiroto/internal/session"
)

// Enqueue marks a session ID for extraction.
func (p *MemoryPipeline) Enqueue(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queued[sessionID] = true
}

func (p *MemoryPipeline) Extract(session *session.Session) error {
	if session == nil {
		return fmt.Errorf("nil session")
	}
	if p.Backend == nil {
		return fmt.Errorf("memory backend not configured")
	}
	entry, err := p.Backend.Extract(session)
	if err != nil {
		return fmt.Errorf("extract session %s: %w", session.ID, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.queued, session.ID)
	p.Entries = append(p.Entries, *entry)
	return nil
}

// Consolidate runs Phase 2: cross-session synthesis from all queued entries.
func (p *MemoryPipeline) Consolidate() error {
	p.mu.Lock()
	if p.Backend == nil {
		p.mu.Unlock()
		return fmt.Errorf("memory backend not configured")
	}
	entriesCopy := make([]*MemoryEntry, len(p.Entries))
	for i, e := range p.Entries {
		entriesCopy[i] = &e
	}
	p.mu.Unlock()

	if len(entriesCopy) == 0 {
		return fmt.Errorf("no memory entries to consolidate")
	}

	// Sort by phase, then by creation time.
	sort.Slice(entriesCopy, func(i, j int) bool {
		if entriesCopy[i].Phase != entriesCopy[j].Phase {
			return entriesCopy[i].Phase < entriesCopy[j].Phase
		}
		return entriesCopy[i].CreatedAt.Before(entriesCopy[j].CreatedAt)
	})

	consolidated, err := p.Backend.Consolidate(entriesCopy)
	if err != nil {
		return fmt.Errorf("consolidate: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.Consolidated = consolidated
	return nil
}

// GetConsolidated returns the latest consolidated memory text.
func (p *MemoryPipeline) GetConsolidated() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Consolidated
}

// InjectIntoPrompt returns memory guidance text suitable for system prompt injection.
func (p *MemoryPipeline) InjectIntoPrompt() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.Consolidated == "" {
		return ""
	}
	return "## Consolidated Memory\n" + p.Consolidated
}

// Clear removes all memory entries and consolidated output.
func (p *MemoryPipeline) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Entries = make([]MemoryEntry, 0)
	p.Consolidated = ""
	p.queued = make(map[string]bool)
}

// queuedIDs returns a sorted slice of queued session IDs (thread-safe).
func (p *MemoryPipeline) queuedIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ids := make([]string, 0, len(p.queued))
	for id := range p.queued {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// queuedCount returns the number of queued session IDs (thread-safe).
func (p *MemoryPipeline) queuedCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.queued)
}

// entryCount returns the number of stored entries (thread-safe).
func (p *MemoryPipeline) entryCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.Entries)
}

// lastEntryAt returns the creation time of the most recent entry (thread-safe).
func (p *MemoryPipeline) lastEntryAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var latest time.Time
	for _, e := range p.Entries {
		if e.CreatedAt.After(latest) {
			latest = e.CreatedAt
		}
	}
	return latest
}
