package tools

import (
	"context"
	"sync"
	"time"

	"github.com/hirotomasato/hiroto/internal/llm"
)

// StreamRule defines a pattern-based rule that triggers text injection
// when matched in LLM token output.
type StreamRule struct {
	ID        string
	Pattern   string
	Rule      string
	Persist   bool
	CreatedAt time.Time
}

// RuleEngine manages a set of stream rules that fire on token patterns
// in LLM output. It supports adding, removing, matching, and retrying
// with rule injection as system reminders.
type RuleEngine struct {
	mu      sync.RWMutex
	Rules   []StreamRule
	History map[string]bool
	RuleLLM    RuleLLM
}

// LLM is the interface that the rule engine uses to call the model.
// It matches llm.Client.Chat signatures so *llm.Client satisfies it.
type RuleLLM interface {
	Chat(ctx context.Context, messages []llm.Message, tools []llm.Tool) (llm.Message, error)
}
// SetLLM sets the underlying LLM client used by ApplyAndRetry.
func (e *RuleEngine) SetLLM(c RuleLLM) {
	e.RuleLLM = c
}
