package tools

import (
	"context"

	"github.com/hirotomasato/hiroto/internal/llm"
)

// CompactionConfig controls auto-compression behaviour.
type CompactionConfig struct {
	Enabled         bool `json:"enabled"`
	Method          string
	ThresholdTokens int  `json:"threshold_tokens"`
	MidTurnEnabled  bool `json:"mid_turn_enabled"`
	AutoContinue    bool `json:"auto_continue"`
}

// CompactionResult reports the outcome of a compaction pass.
type CompactionResult struct {
	Method       string `json:"method"`
	TokensBefore int    `json:"tokens_before"`
	TokensAfter  int    `json:"tokens_after"`
	Summary      string `json:"summary"`
}

// CompactionEntry stores a compaction checkpoint so the engine can
// reconstruct context later (e.g. after a session resume).
type CompactionEntry struct {
	ID             string            `json:"id"`
	Summary        string            `json:"summary"`
	ShortSummary   string            `json:"short_summary"`
	FirstKeptEntryID string          `json:"first_kept_entry_id"`
	TokensBefore   int               `json:"tokens_before"`
	Details        string            `json:"details"`
	PreserveData   map[string]any    `json:"preserve_data,omitempty"`
}

// Compressor implements a single compaction strategy.
type Compressor interface {
	Compress(ctx context.Context, msgs []llm.Message, prevSummary string) (string, CompactionResult, error)
}
